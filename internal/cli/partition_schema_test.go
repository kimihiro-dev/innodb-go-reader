package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func trustedPartitionPaths(t *testing.T) (string, []string) {
	t.Helper()
	root := "../../testdata/partition-patches/5.7.28"
	var m struct {
		Cases []struct {
			Name  string
			Files []struct{ File string }
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var paths []string
	for _, c := range m.Cases {
		if c.Name == "ranges_dynamic_current" {
			for _, f := range c.Files {
				paths = append(paths, fixture(t, "partition-patches/5.7.28/"+f.File))
			}
		}
	}
	b, err = os.ReadFile(filepath.Join(root, "ranges_dynamic_current.partitions.json"))
	if err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(dir, "partitions.json")
	if err := os.WriteFile(definition, b, 0600); err != nil {
		t.Fatal(err)
	}
	return definition, paths
}

func TestTrustedPartitionCLI(t *testing.T) {
	definition, paths := trustedPartitionPaths(t)
	dest := filepath.Join(filepath.Dir(definition), "files.json")
	args := append([]string{"manifest", "--partition-schema", definition, "--output", dest}, paths...)
	if code, out, s := run(t, args...); code != 0 || !s.Complete || len(out) != 0 {
		t.Fatal("manifest", code, s)
	}
	m, err := readManifest(dest)
	if err != nil || m.Version != 2 || m.Schema != definition || len(m.Files) != 3 {
		t.Fatal("v2 manifest", err, m)
	}
	for _, command := range []string{"metadata", "export", "check"} {
		if code, out, s := run(t, command, "--manifest", dest); code != 0 || !s.Complete || len(out) == 0 {
			t.Fatal(command, code, s)
		}
	}
	if code, out, s := run(t, "export", "--manifest", dest, "--max-rows", "1"); code != 1 || s.Complete || bytes.Contains(out, []byte(`"kind":"end"`)) {
		t.Fatal("streamed limit", code, s)
	}
	before, _ := os.ReadFile(definition)
	if code, _, _ := run(t, "metadata", "--manifest", dest, "--output", definition, "--overwrite"); code != 1 {
		t.Fatal("schema overwritten")
	}
	after, _ := os.ReadFile(definition)
	if !bytes.Equal(before, after) {
		t.Fatal("schema changed")
	}
	if code, _, _ := run(t, append([]string{"manifest", "--partition-schema", definition, "--output", definition, "--overwrite"}, paths...)...); code != 1 {
		t.Fatal("manifest overwrote definition")
	}
	for _, bad := range [][]string{paths[:2], {paths[0], paths[0], paths[2]}} {
		output := filepath.Join(t.TempDir(), "files.json")
		if code, out, s := run(t, append([]string{"manifest", "--partition-schema", definition, "--output", output}, bad...)...); code != 1 || s.Complete || len(out) != 0 {
			t.Fatal("bad collection", code, s)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("published invalid manifest")
		}
	}
	if code, _, _ := run(t, append([]string{"manifest", "--partition-schema", definition, "--max-page-reads", "1"}, paths...)...); code != 1 {
		t.Fatal("budget")
	}
}

func TestTrustedManifestDefinitionSyntax(t *testing.T) {
	definition, paths := trustedPartitionPaths(t)
	for _, raw := range []string{`{"version":2,"version":2,"schema":"partitions.json","files":[]}`, `{"version":2,"schema":null,"files":[]}`, `{"version":1,"schema":"partitions.json","files":[]}`} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		os.WriteFile(path, []byte(raw), 0600)
		if _, err := readManifest(path); err == nil {
			t.Fatal("invalid manifest accepted", raw)
		}
	}
	if err := os.WriteFile(definition, []byte(`{"version":1,"Version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out, s := run(t, append([]string{"manifest", "--partition-schema", definition}, paths...)...); code != 1 || s.Complete || len(out) != 0 {
		t.Fatal("duplicate trusted keys accepted", code, s)
	}
}
