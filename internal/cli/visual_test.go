package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVisualCommands(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, args := range [][]string{{"visualize", input}, {"visualize", "--index", "PRIMARY", "--pages", "0,4", input}} {
		code, b, s := run(t, args...)
		if code != 0 || !s.Complete || !bytes.Contains(b, []byte("<!doctype html>")) {
			t.Fatal(code, s)
		}
		if !strings.Contains(string(b), `id="report-data"`) {
			t.Fatal("missing data")
		}
	}
	target := filepath.Join(t.TempDir(), "report.html")
	if code, _, s := run(t, "visualize", "--output", target, input); code != 0 {
		t.Fatal(s)
	}
	before, _ := os.ReadFile(target)
	if code, _, _ := run(t, "visualize", "--output", target, "--overwrite", "--index", "PRIMARY", "--max-rows", "1", input); code != 1 {
		t.Fatal("failed scan")
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Fatal("failed overwrite")
	}
	if code, _, _ := run(t, "visualize", "--output", input, "--overwrite", input); code != 1 {
		t.Fatal("input protection")
	}
	for _, args := range [][]string{{"visualize", "--pages", "4,4", input}, {"visualize", "--pages", "bad", input}, {"visualize", "--materialized", input}, {"visualize", "--max-rows", "1", input}} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Fatal("usage", args, code)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"visualize", "--manifest", "x"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatal("unknown flag usage")
	}
	for _, args := range [][]string{{"visualize", "--pages", "999", input}, {"visualize", "--index", "missing", input}, {"visualize", "--max-report-bytes", "1", input}, {"visualize", "--max-read-calls", "1", input}} {
		if code, b, s := run(t, args...); code != 1 || len(b) != 0 || s.Complete {
			t.Fatal("runtime/preflight output", args, code, s)
		}
	}
}
