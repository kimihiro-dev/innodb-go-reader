package cli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	innodb "innodb-go-reader"
	"innodb-go-reader/rowio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func partitionManifestFixture(t *testing.T, name string) string {
	t.Helper()
	root := "../../testdata/partitions"
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name  string
			Files []struct{ Partition, File string }
		}
	}
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifest := partitionManifest{Version: 1}
	for _, c := range m.Cases {
		if c.Name != name {
			continue
		}
		for _, item := range c.Files {
			f, err := os.Open(filepath.Join(root, item.File))
			if err != nil {
				t.Fatal(err)
			}
			z, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(z)
			z.Close()
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			path := item.Partition + ".ibd"
			if err = os.WriteFile(filepath.Join(dir, path), raw, 0600); err != nil {
				t.Fatal(err)
			}
			manifest.Files = append(manifest.Files, struct {
				Partition string `json:"partition"`
				Path      string `json:"path"`
			}{item.Partition, path})
		}
	}
	if len(manifest.Files) == 0 {
		t.Fatal("missing fixture")
	}
	// Explicit file lists are independent of filename ordering.
	for i, j := 0, len(manifest.Files)-1; i < j; i, j = i+1, j-1 {
		manifest.Files[i], manifest.Files[j] = manifest.Files[j], manifest.Files[i]
	}
	raw, _ := json.Marshal(manifest)
	path := filepath.Join(dir, "files.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPartitionCommands(t *testing.T) {
	manifest := partitionManifestFixture(t, "ranges")
	code, b, s := run(t, "metadata", "--manifest", manifest)
	if code != 0 {
		t.Fatal(s.Error)
	}
	var set innodb.PartitionSet
	if err := json.Unmarshal(b, &set); err != nil || len(set.Partitions) != 3 {
		t.Fatal("metadata", err)
	}
	for _, format := range []string{"jsonl", "csv"} {
		code, b, s := run(t, "export", "--manifest", manifest, "--format", format)
		if code != 0 {
			t.Fatal(s.Error)
		}
		h, rows := readRows(t, b, format)
		if !h.Physical || len(rows) != 1000 {
			t.Fatal("export")
		}
		for _, row := range rows {
			var p struct {
				Partition innodb.PartitionSource `json:"partition"`
				Record    json.RawMessage        `json:"record"`
			}
			if err := json.Unmarshal(row.Physical, &p); err != nil || p.Partition.Name == "" || len(p.Record) != 0 {
				t.Fatal("mandatory source/optional record", err)
			}
		}
	}
	code, b, s = run(t, "check", "--manifest", manifest)
	if code != 0 {
		t.Fatal(s.Error)
	}
	var report innodb.PartitionScanReport
	json.Unmarshal(b, &report)
	if !report.Complete || report.CompletedPartitions != 3 || report.Records != 1000 {
		t.Fatal(report)
	}
	virtual := partitionManifestFixture(t, "virtual_rows")
	if code, _, _ := run(t, "export", "--manifest", virtual); code != 1 {
		t.Fatal("VIRTUAL accepted")
	}
	if code, _, s := run(t, "export", "--manifest", virtual, "--materialized"); code != 0 {
		t.Fatal(s.Error)
	}
	for _, args := range [][]string{{"query", "--manifest", manifest}, {"check", "--manifest", manifest, "--scope", "space"}, {"export", "--manifest", manifest, "file.ibd"}} {
		var out, diag bytes.Buffer
		if code := Run(t.Context(), args, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatal("usage", code)
		}
	}
}
func TestPartitionManifestFailures(t *testing.T) {
	manifest := partitionManifestFixture(t, "ranges")
	dir := filepath.Dir(manifest)
	dest := filepath.Join(dir, "out")
	os.WriteFile(dest, []byte("old"), 0600)
	code, _, _ := run(t, "export", "--manifest", manifest, "--max-rows", "501", "--output", dest, "--overwrite")
	if code != 1 {
		t.Fatal("budget")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "old" {
		t.Fatal("partial replaced target")
	}
	for _, target := range []string{manifest, filepath.Join(dir, "p1.ibd")} {
		before, _ := os.ReadFile(target)
		code, _, _ := run(t, "export", "--manifest", manifest, "--output", target, "--overwrite")
		after, _ := os.ReadFile(target)
		if code != 1 || !bytes.Equal(before, after) {
			t.Fatal("input protection")
		}
	}
	m, err := readManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	m.Files = m.Files[:2]
	raw, _ := json.Marshal(m)
	os.WriteFile(manifest, raw, 0600)
	code, b, _ = run(t, "export", "--manifest", manifest)
	if code != 1 || len(b) != 0 {
		t.Fatal("missing partition emitted schema")
	}
	for _, raw := range []string{`null`, `{"version":2,"files":[]}`, `{"version":1,"files":[],"bad":true}`, `{} {}`} {
		os.WriteFile(manifest, []byte(raw), 0600)
		if code, _, _ := run(t, "metadata", "--manifest", manifest); code != 2 {
			t.Fatal("manifest format")
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".innodb-reader-*"))
	if len(matches) != 0 {
		t.Fatal("temporary output leaked")
	}
}

func TestPartitionManifestSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.json")
	valid := `{"version":1,"files":[{"partition":"p0","path":"p.ibd"}]}`
	for _, b := range []string{valid + strings.Repeat(" ", 1<<20), strings.Repeat(" ", 1<<20) + valid, valid + "\xff"} {
		if err := os.WriteFile(path, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readManifest(path); err == nil {
			t.Fatal("accepted oversized/invalid manifest")
		}
	}
}

func TestPartitionPartialOutput(t *testing.T) {
	manifest := partitionManifestFixture(t, "ranges")
	for _, format := range []string{"jsonl", "csv"} {
		code, b, s := run(t, "export", "--manifest", manifest, "--format", format, "--max-rows", "501")
		if code != 1 || s.Complete || len(b) == 0 {
			t.Fatal("partial output state", code, s)
		}
		d, err := rowio.NewDecoder(bytes.NewReader(b), format, rowio.Options{})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for {
			_, err = d.Next()
			if err != nil {
				break
			}
			count++
		}
		if count != 501 || d.Complete() {
			t.Fatal("partial output rows/completion", count, err)
		}
		if format == "jsonl" && err == io.EOF {
			t.Fatal("missing end accepted")
		}
	}
}
