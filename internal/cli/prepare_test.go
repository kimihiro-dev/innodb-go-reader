package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratePartitionManifest(t *testing.T) {
	for _, name := range []string{"ranges", "lists", "hashes", "keys_table", "range_empty", "range_rebuilt_nonanchor", "hidden_rows"} {
		t.Run(name, func(t *testing.T) {
			old := partitionManifestFixture(t, name)
			m, err := readManifest(old)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for i := len(m.Files) - 1; i >= 0; i-- {
				paths = append(paths, m.Files[i].Path)
			}
			// Names come from SDI even when filenames contain no partition name.
			for i := range paths {
				next := filepath.Join(filepath.Dir(paths[i]), string(rune('a'+i))+".ibd")
				if err = os.Rename(paths[i], next); err != nil {
					t.Fatal(err)
				}
				paths[i] = next
			}
			dest := filepath.Join(filepath.Dir(old), "generated.json")
			args := append([]string{"manifest", "--output", dest}, paths...)
			code, out, s := run(t, args...)
			if code != 0 || !s.Complete || len(out) != 0 || s.PreflightReads == 0 {
				t.Fatal(code, s, string(out))
			}
			generated, err := readManifest(dest)
			if err != nil || len(generated.Files) != len(paths) {
				t.Fatal(err)
			}
			for _, command := range []string{"metadata", "check", "export"} {
				code, _, s := run(t, command, "--manifest", dest)
				if code != 0 || !s.Complete {
					t.Fatal(command, code, s)
				}
			}
			code, out, s = run(t, append([]string{"manifest"}, paths...)...)
			if code != 0 || !s.Complete {
				t.Fatal(code, s)
			}
			var stdoutManifest partitionManifest
			if json.Unmarshal(out, &stdoutManifest) != nil {
				t.Fatal("invalid stdout manifest")
			}
			for _, item := range stdoutManifest.Files {
				if !filepath.IsAbs(item.Path) {
					t.Fatal("stdout paths must be absolute")
				}
			}
			var raw partitionManifest
			b, _ := os.ReadFile(dest)
			json.Unmarshal(b, &raw)
			for _, item := range raw.Files {
				if filepath.IsAbs(item.Path) {
					t.Fatal("file paths should be relative to manifest directory")
				}
			}
		})
	}
}

func TestGenerateManifestFailures(t *testing.T) {
	mPath := partitionManifestFixture(t, "ranges")
	m, _ := readManifest(mPath)
	paths := []string{}
	for _, item := range m.Files {
		paths = append(paths, item.Path)
	}
	other := partitionManifestFixture(t, "lists")
	otherM, _ := readManifest(other)
	nonpartition := fixture(t, "mysql5744/scalar_dynamic_initial.ibd.gz")
	bad := filepath.Join(t.TempDir(), "damaged.ibd")
	b, _ := os.ReadFile(paths[0])
	b[200] ^= 1
	os.WriteFile(bad, b, 0600)
	for _, inputs := range [][]string{paths[:2], {paths[0], paths[0], paths[2]}, {paths[0], paths[1], otherM.Files[0].Path}, {nonpartition}, {bad, paths[1], paths[2]}} {
		dest := filepath.Join(t.TempDir(), "files.json")
		code, out, s := run(t, append([]string{"manifest", "--output", dest}, inputs...)...)
		if code != 1 || s.Complete || len(out) != 0 {
			t.Fatal("invalid collection", code, s)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("published invalid manifest")
		}
	}
	if code, _, s := run(t, append([]string{"manifest", "--max-page-reads", "1"}, paths...)...); code != 1 || s.Complete {
		t.Fatal("budget", code, s)
	}
	before, _ := os.ReadFile(paths[0])
	if code, _, _ := run(t, append([]string{"manifest", "--output", paths[0], "--overwrite"}, paths...)...); code != 1 {
		t.Fatal("input overwrite accepted")
	}
	after, _ := os.ReadFile(paths[0])
	if !bytes.Equal(before, after) {
		t.Fatal("input changed")
	}
	dest := filepath.Join(t.TempDir(), "files.json")
	os.WriteFile(dest, []byte("old"), 0600)
	if code, _, _ := run(t, append([]string{"manifest", "--output", dest}, paths...)...); code != 1 {
		t.Fatal("overwrite accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diag bytes.Buffer
	if code := Run(ctx, append([]string{"manifest"}, paths...), &out, &diag); code != 130 || out.Len() != 0 {
		t.Fatal("cancellation", code)
	}
}

func TestInputPreparationUsage(t *testing.T) {
	for _, args := range [][]string{{"schema"}, {"schema", "--database", "d", "--table", "t"}, {"schema", "--database", "d", "--table", "t", "--output", "dir", "--overwrite"}, {"manifest"}, {"manifest", "--schema", "schema.json", "p.ibd"}} {
		var out, diag bytes.Buffer
		if code := Run(t.Context(), args, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatal("invalid usage", args, code)
		}
	}
	for _, command := range []string{"schema", "manifest"} {
		var out, diag bytes.Buffer
		if code := Run(t.Context(), []string{command, "--help"}, &out, &diag); code != 0 || out.Len() != 0 {
			t.Fatal("help", code)
		}
	}
	dest := t.TempDir()
	if code, _, _ := run(t, "schema", "--database", "d", "--table", "t", "--output", dest); code != 1 {
		t.Fatal("existing schema output directory accepted")
	}
}

func TestSchemaPreparationFailureCleanup(t *testing.T) {
	// No executable can be found, proving schema no longer invokes a client process.
	t.Setenv("PATH", t.TempDir())
	parent := t.TempDir()
	dest := filepath.Join(parent, "definitions")
	args := []string{"schema", "--socket", filepath.Join(parent, "missing.sock"), "--timeout", "1s", "--database", "d", "--table", "t", "--output", dest}
	if code, out, s := run(t, args...); code != 1 || s.Complete || len(out) != 0 {
		t.Fatal("connection failure", code, s)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed collection left files", err, entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diag bytes.Buffer
	if code := Run(ctx, args, &out, &diag); code != 130 || out.Len() != 0 {
		t.Fatal("cancellation", code)
	}
	entries, err = os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled collection left files", err, entries)
	}
}

func TestSchemaDriverUsage(t *testing.T) {
	for _, flags := range [][]string{{"--container", "legacy"}, {"--snapshot"}, {"--socket", "sock", "--host", "host"}, {"--socket", "sock", "--port", "3306"}, {"--tls-ca", "ca.pem"}, {"--timeout", "0s"}, {"--port", "65536"}} {
		args := append([]string{"schema", "--database", "d", "--table", "t", "--output", "out"}, flags...)
		var out, diag bytes.Buffer
		if code := Run(t.Context(), args, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatal("invalid/removed option", flags, code)
		}
	}
}
