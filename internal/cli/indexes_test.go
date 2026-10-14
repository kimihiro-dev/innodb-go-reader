package cli

import (
	"bytes"
	"context"
	"encoding/json"
	innodb "innodb-go-reader"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitSecondaryCLI(t *testing.T) {
	input := fixture(t, "mysql5744-secondary/ordinary_dynamic.ibd.gz")
	schema := "../../testdata/mysql5744-secondary/ordinary_dynamic.json"
	indexes := "../../testdata/mysql5744-secondary/ordinary_dynamic.indexes.json"
	code, out, s := run(t, "metadata", "--schema", schema, "--indexes", indexes, input)
	var meta struct {
		Source  string
		Schema  innodb.Schema
		Indexes []innodb.SecondarySchema
	}
	if code != 0 || s.PreflightReads != 8 || json.Unmarshal(out, &meta) != nil || meta.Source != "explicit_schema" || len(meta.Indexes) != 5 {
		t.Fatal(code, s, string(out))
	}
	code, out, s = run(t, "check", "--scope", "secondary", "--schema", schema, "--indexes", indexes, "--index", "s", input)
	var scan innodb.SecondaryScanReport
	if code != 0 || s.PreflightReads != 8 || json.Unmarshal(out, &scan) != nil || scan.Records != 7 || !scan.Complete {
		t.Fatal(code, s)
	}
	query := filepath.Join(t.TempDir(), "query.json")
	for _, c := range []struct {
		raw     string
		rows    int
		covered bool
	}{
		{`{"Range":{"Prefix":[-1]},"Columns":["n","id"]}`, 2, true},
		{`{"Range":{"Prefix":[-1]}}`, 2, false},
		{`{"Range":{"Prefix":[null]},"Columns":["n","id"]}`, 2, true},
		{`{"Range":{"Reverse":true,"Limit":3}}`, 3, false},
	} {
		if err := os.WriteFile(query, []byte(c.raw), 0600); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"jsonl", "csv"} {
			code, out, s = run(t, "query", "--schema", schema, "--indexes", indexes, "--index", "s", "--query", query, "--format", format, "--physical", input)
			if code != 0 || s.PreflightReads != 8 {
				t.Fatal(code, s)
			}
			_, rows := readRows(t, out, format)
			if len(rows) != c.rows {
				t.Fatal("projection", len(rows))
			}
			for _, row := range rows {
				var physical struct {
					Covered       bool
					ClusteredPage *uint32
					SecondaryPage uint32
				}
				if json.Unmarshal(row.Physical, &physical) != nil || physical.Covered != c.covered || (physical.ClusteredPage == nil) != c.covered || physical.SecondaryPage == 0 {
					t.Fatal("provenance", string(row.Physical))
				}
			}
		}
	}
	// Prefix equality must compare the original value after fetching candidates.
	_ = os.WriteFile(query, []byte(`{"Range":{"Prefix":["abc-two"]},"Columns":["id"]}`), 0600)
	code, out, s = run(t, "query", "--schema", schema, "--indexes", indexes, "--index", "px", "--query", query, input)
	if code != 0 {
		t.Fatal(code, s)
	}
	_, rows := readRows(t, out, "jsonl")
	if len(rows) != 1 || rows[0].Values[0] != int32(4) {
		t.Fatal(rows)
	}
	for _, index := range []string{"PRIMARY", "s", "px"} {
		code, out, s = run(t, "visualize", "--schema", schema, "--indexes", indexes, "--index", index, "--pages", "3,4", input)
		if code != 0 || !s.Complete || !bytes.Contains(out, []byte(`"MetadataSource":"explicit_schema"`)) {
			t.Fatal(code, s)
		}
	}
}
func TestExplicitSecondaryFailures(t *testing.T) {
	input := fixture(t, "mysql5744-secondary/ordinary_dynamic.ibd.gz")
	schema := "../../testdata/mysql5744-secondary/ordinary_dynamic.json"
	indexes := "../../testdata/mysql5744-secondary/ordinary_dynamic.indexes.json"
	query := filepath.Join(t.TempDir(), "query.json")
	_ = os.WriteFile(query, []byte(`{"Range":{}}`), 0600)
	for _, raw := range []string{`null`, `{}`, `[null]`, `[{"Name":"s","name":"t"}]`, `[{"Fields":[{"Column":1,"Definition":{"nullable":null}}]}]`, `[{"Unknown":1}]`, `[] []`, `[{"ſpaceID":1}]`, "[\"\xff\"]", strings.Repeat(" ", (1<<20)+1)} {
		path := filepath.Join(t.TempDir(), "indexes.json")
		_ = os.WriteFile(path, []byte(raw), 0600)
		target := filepath.Join(t.TempDir(), "out")
		code, _, s := run(t, "metadata", "--schema", schema, "--indexes", path, "--output", target, input)
		if code == 0 || s.Complete {
			t.Fatal("bad input", raw)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("bad publication")
		}
	}
	for _, args := range [][]string{
		{"metadata", "--indexes", indexes, input},
		{"metadata", "--schema", schema, "--indexes=", input},
		{"check", "--schema", schema, "--indexes", indexes, input},
		{"query", "--schema", schema, "--indexes", indexes, "--query", query, input},
		{"visualize", "--schema", schema, input},
		{"visualize", "--indexes", indexes, "--index", "s", input},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Fatal(args, code)
		}
	}
	for _, args := range [][]string{
		{"query", "--schema", schema, "--indexes", indexes, "--index", "missing", "--query", query, input},
		{"check", "--scope", "secondary", "--schema", schema, "--indexes", indexes, "--index", "s", "--max-page-reads", "8", input},
		{"query", "--schema", schema, "--indexes", indexes, "--index", "s", "--query", query, "--max-page-reads", "8", input},
		{"visualize", "--schema", schema, "--indexes", indexes, "--index", "s", "--max-page-reads", "8", input},
		{"visualize", "--schema", schema, "--indexes", indexes, "--index", "missing", input},
	} {
		if code, out, s := run(t, args...); code != 1 || len(out) != 0 || s.Complete {
			t.Fatal(args, code, s, len(out))
		}
	}
	before, _ := os.ReadFile(indexes)
	copy := filepath.Join(t.TempDir(), "indexes.json")
	_ = os.WriteFile(copy, before, 0600)
	if code, _, _ := run(t, "metadata", "--schema", schema, "--indexes", copy, "--output", copy, "--overwrite", input); code != 1 {
		t.Fatal("input overwrite")
	}
	after, _ := os.ReadFile(copy)
	if !bytes.Equal(before, after) {
		t.Fatal("input changed")
	}
	alias := filepath.Join(t.TempDir(), "alias.json")
	if err := os.Link(copy, alias); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "metadata", "--schema", schema, "--indexes", copy, "--output", alias, "--overwrite", input); code != 1 {
		t.Fatal("hardlink overwrite")
	}
	// Failure must preserve an existing output even after the row header is written.
	target := filepath.Join(t.TempDir(), "existing")
	_ = os.WriteFile(target, []byte("keep"), 0600)
	if code, _, _ := run(t, "query", "--schema", schema, "--indexes", indexes, "--index", "s", "--query", query, "--max-rows", "1", "--output", target, "--overwrite", input); code != 1 {
		t.Fatal("budget")
	}
	after, _ = os.ReadFile(target)
	if string(after) != "keep" {
		t.Fatal("atomic failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, stderr bytes.Buffer
	if code := Run(ctx, []string{"check", "--scope", "secondary", "--schema", schema, "--indexes", indexes, "--index", "s", input}, &out, &stderr); code != 130 || out.Len() != 0 {
		t.Fatal("cancel", code)
	}
}
func TestExplicitSecondaryVirtual(t *testing.T) {
	input := fixture(t, "mysql5744-secondary/generated_dynamic.ibd.gz")
	schema := "../../testdata/mysql5744-secondary/generated_dynamic.json"
	indexes := "../../testdata/mysql5744-secondary/generated_dynamic.indexes.json"
	if code, _, s := run(t, "check", "--scope", "secondary", "--schema", schema, "--indexes", indexes, "--index", "secondary", input); code != 0 {
		t.Fatal(code, s)
	}
	query := filepath.Join(t.TempDir(), "query.json")
	_ = os.WriteFile(query, []byte(`{"Range":{}}`), 0600)
	if code, _, _ := run(t, "query", "--schema", schema, "--indexes", indexes, "--index", "secondary", "--query", query, input); code != 1 {
		t.Fatal("VIRTUAL requires explicit view")
	}
	if code, out, s := run(t, "query", "--materialized", "--schema", schema, "--indexes", indexes, "--index", "secondary", "--query", query, input); code != 0 {
		t.Fatal(code, s)
	} else {
		header, rows := readRows(t, out, "jsonl")
		if len(rows) != 3 || len(header.VirtualColumns) != 1 {
			t.Fatal("materialized view")
		}
	}
}
func FuzzExplicitIndexes(f *testing.F) {
	for _, raw := range []string{`[]`, `null`, `[{"Name":"s"}]`, `[{"Name":"s","name":"t"}]`, `[null]`} {
		f.Add(raw)
	}
	path := filepath.Join(f.TempDir(), "indexes.json")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<20 {
			return
		}
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = readIndexes(path)
	})
}
