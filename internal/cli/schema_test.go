package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExplicitSchemaCLI(t *testing.T) {
	input := fixture(t, "mysql5744/scalar_dynamic_initial.ibd.gz")
	schema := "../../testdata/mysql5744/scalar_dynamic_initial.json"
	for _, format := range []string{"jsonl", "csv"} {
		code, out, s := run(t, "export", "--schema", schema, "--format", format, input)
		if code != 0 || !s.Complete || s.PreflightReads != 2 {
			t.Fatal(code, s)
		}
		header, rows := readRows(t, out, format)
		if len(rows) != 3 || len(header.Columns) != 34 || rows[0].Values[10] != uint64(18446744073709551615) || rows[1].Values[10] != nil {
			t.Fatal("lossless values/SQL NULL", rows)
		}
	}
	if code, _, s := run(t, "check", "--schema", schema, input); code != 0 || s.PreflightReads != 2 {
		t.Fatal(code, s)
	}
	code, out, s := run(t, "metadata", "--schema", schema, input)
	var meta struct {
		Source string
		Schema json.RawMessage
	}
	if code != 0 || s.Scope != "explicit schema and clustered root identity" || json.Unmarshal(out, &meta) != nil || meta.Source != "explicit_schema" || len(meta.Schema) == 0 {
		t.Fatal(code, s, string(out))
	}
	query := filepath.Join(t.TempDir(), "query.json")
	if err := os.WriteFile(query, []byte(`{"Lower":{"Key":[1],"Inclusive":true},"Upper":{"Key":[3],"Inclusive":true},"Reverse":true,"Limit":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, s = run(t, "query", "--schema", schema, "--query", query, input)
	if code != 0 {
		t.Fatal(code, s)
	}
	_, rows := readRows(t, out, "jsonl")
	if len(rows) != 1 || rows[0].Values[0] != int32(3) {
		t.Fatal("clustered range/reverse/limit", rows)
	}
}

func TestExplicitSchemaFailures(t *testing.T) {
	input := fixture(t, "mysql5744/scalar_dynamic_initial.ibd.gz")
	good := "../../testdata/mysql5744/scalar_dynamic_initial.json"
	for _, raw := range []string{`null`, `{}`, `{"root_page":3,"root_page":4}`, `{"columns":[{"name":"x","Name":"y"}]}`, `{"columns":[{"nullable":null}]}`, `{"unknown":1}`, `{} {}`, "{\"name\":\"\xff\"}", strings.Repeat(" ", (1<<20)+1)} {
		schema := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(schema, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "rows.jsonl")
		code, _, s := run(t, "export", "--schema", schema, "--output", dest, input)
		if code == 0 || s.Complete {
			t.Fatal("accepted malformed/invalid schema", raw)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("published invalid result")
		}
	}
	for _, args := range [][]string{
		{"export", "--schema", good, "--manifest", "missing.json"},
		{"query", "--schema", good, "--index", "secondary", "--query", "missing.json", input},
		{"check", "--schema", good, "--scope", "secondary", "--index", "secondary", input},
		{"check", "--schema", good, "--scope", "space", input},
		{"export", "--schema=", input},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Fatal("ambiguous/unimplemented option combination", args, code)
		}
	}
	copySchema := filepath.Join(t.TempDir(), "schema.json")
	b, err := os.ReadFile(good)
	if err != nil || os.WriteFile(copySchema, b, 0600) != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "export", "--schema", copySchema, "--output", copySchema, "--overwrite", input); code != 1 {
		t.Fatal("schema input overwrite")
	}
	if after, err := os.ReadFile(copySchema); err != nil || string(after) != string(b) {
		t.Fatal("schema input changed", err)
	}
	alias := filepath.Join(t.TempDir(), "alias.json")
	if err := os.Link(copySchema, alias); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "export", "--schema", copySchema, "--output", alias, "--overwrite", input); code != 1 {
		t.Fatal("schema input alias overwrite")
	}
	if code, out, s := run(t, "export", "--schema", good, "--max-page-reads", "2", input); code != 1 || len(out) != 0 || !strings.Contains(s.Error, "budget") {
		t.Fatal("schema preflight budget", code, s)
	}
	if code, _, s := run(t, "export", input); code != 1 || !strings.Contains(s.Error, "SDI") {
		t.Fatal("auto 5.7 discovery", code, s)
	}
}

func FuzzReadSchema(f *testing.F) {
	for _, seed := range []string{`{}`, `{"columns":[{"name":"a","Name":"b"}]}`, `null`, `{} {}`, "\xff"} {
		f.Add([]byte(seed))
	}
	seed, err := os.ReadFile("../../testdata/mysql5744/scalar_dynamic_initial.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > (1<<20)+1 {
			return
		}
		path := filepath.Join(t.TempDir(), "schema.json")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := readSchema(path)
		if err == nil && (!json.Valid(b) || !utf8.Valid(b)) {
			t.Fatal("accepted malformed input")
		}
	})
}

func TestExplicitSchemaVirtual(t *testing.T) {
	input := fixture(t, "mysql5744/generated_compact_initial.ibd.gz")
	schema := "../../testdata/mysql5744/generated_compact_initial.json"
	if code, _, _ := run(t, "export", "--schema", schema, input); code != 1 {
		t.Fatal("full VIRTUAL export must fail")
	}
	code, out, s := run(t, "export", "--schema", schema, "--materialized", input)
	if code != 0 {
		t.Fatal(code, s)
	}
	header, rows := readRows(t, out, "jsonl")
	if len(header.VirtualColumns) != 1 || len(header.Columns) != 3 || len(rows) != 2 || rows[0].Values[2] != int32(8) {
		t.Fatal("materialized generated view", header, rows)
	}
}

func TestSchemaUnicodeAlias(t *testing.T) {
	b, err := os.ReadFile("../../testdata/mysql5744/scalar_dynamic_initial.json")
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json folds the Unicode long s to ASCII S when matching tags.
	b = []byte(strings.TrimSuffix(strings.TrimSpace(string(b)), "}") + `,"ſpace_id":43}`)
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSchema(path); err == nil {
		t.Fatal("accepted two spellings of the same physical attribute")
	}
}
