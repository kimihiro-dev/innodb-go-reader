package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatsViews(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, view := range []string{"summary", "page-types", "indexes", "levels", "segments", "regions"} {
		t.Run(view, func(t *testing.T) {
			for _, format := range []string{"table", "json"} {
				code, out, s := run(t, "stats", "--view", view, "--format", format, input)
				if code != 0 {
					t.Fatal(s.Error)
				}
				if s.Command != "stats" || !strings.Contains(s.Scope, "whole space") {
					t.Fatal(s)
				}
				proof, _ := json.Marshal(s.Report)
				var diag statsSummary
				if err := json.Unmarshal(proof, &diag); err != nil {
					t.Fatal(err)
				}
				if diag.View != view || diag.PhysicalReads != 7 || diag.TraversalEntries == nil || *diag.TraversalEntries != 104 {
					t.Fatal("analysis counters", diag)
				}
				if format == "json" {
					dec := json.NewDecoder(bytes.NewReader(out))
					var report map[string]any
					if err := dec.Decode(&report); err != nil {
						t.Fatal(err)
					}
					if report["view"] != view || report["complete"] != true || report["schema_version"] != float64(1) {
						t.Fatal(report)
					}
					if err := dec.Decode(&report); err != io.EOF {
						t.Fatal("stdout is not one JSON object", err)
					}
					validation := report["validation"].(map[string]any)
					if validation["tree_navigation"] != false || validation["row_values"] != false {
						t.Fatal("scope overclaim")
					}
				} else if !strings.Contains(string(out), "view="+view) || strings.HasPrefix(string(out), "{") {
					t.Fatal("table", string(out))
				}
			}
		})
	}
	code, out, s := run(t, "stats", input)
	if code != 0 || !strings.Contains(string(out), "used_share") || !strings.Contains(string(out), "71.43%") {
		t.Fatal("default summary/table", s)
	}
	for _, view := range []string{"indexes", "levels", "segments"} {
		code, out, s := run(t, "stats", "--view", view, "--index-id", "18446744073709551615", "--format", "json", input)
		if code != 0 || !bytes.Contains(out, []byte(`"index_id":"18446744073709551615"`)) {
			t.Fatal("SDI precision", s)
		}
	}
	code, out, s = run(t, "stats", "--view", "levels", "--index-id", "249", "--level", "0", "--format", "json", input)
	if code != 0 || !bytes.Contains(out, []byte(`"level":"0"`)) || bytes.Contains(out, []byte("18446744073709551615")) {
		t.Fatal("leaf filter", s, string(out))
	}
}

func TestStatsUsageAndMissingFilters(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	invalid := [][]string{
		{"--view", ""}, {"--view", "histogram"}, {"--format", "csv"}, {"--index-id", "249"},
		{"--view", "regions", "--index-id", "249"}, {"--view", "page-types", "--index-id", "249"},
		{"--view", "indexes", "--index-id", "0"}, {"--view", "indexes", "--index-id", ""},
		{"--view", "indexes", "--index-id", "18446744073709551616"},
		{"--view", "indexes", "--index-id", "-1"}, {"--view", "indexes", "--index-id", "+249"},
		{"--view", "indexes", "--index-id", "0xf9"}, {"--view", "indexes", "--index-id", " 249"},
		{"--view", "levels", "--level", "0"}, {"--view", "segments", "--index-id", "249", "--level", "0"},
		{"--view", "levels", "--index-id", "249", "--level", "65536"},
		{"--view", "levels", "--index-id", "249", "--level", "-1"},
		{"--view", "levels", "--index-id", "249", "--level", ""},
		{"--overwrite"}, {"--materialized"}, {"--max-rows", "5"}, {"--manifest", "files.json"}, {"--index", "PRIMARY"},
	}
	for _, flags := range invalid {
		var out, diag bytes.Buffer
		args := append([]string{"stats"}, flags...)
		args = append(args, input)
		if code := Run(context.Background(), args, &out, &diag); code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), `"complete":false`) {
			t.Fatal(flags, code, out.String(), diag.String())
		}
	}
	for _, flags := range [][]string{{"--view", "indexes", "--index-id", "123"}, {"--view", "segments", "--index-id", "123"}, {"--view", "levels", "--index-id", "249", "--level", "1"}} {
		args := append([]string{"stats"}, flags...)
		args = append(args, input)
		code, out, s := run(t, args...)
		if code != 1 || len(out) != 0 || !strings.Contains(s.Error, "not found") {
			t.Fatal(flags, code, s)
		}
		proof, _ := json.Marshal(s.Report)
		var diag statsSummary
		if err := json.Unmarshal(proof, &diag); err != nil {
			t.Fatal(err)
		}
		if diag.PhysicalReads != 7 || diag.TraversalEntries != nil {
			t.Fatal("error counter/null", diag)
		}
	}
	var out, diag bytes.Buffer
	if code := Run(context.Background(), []string{"stats", "--help"}, &out, &diag); code != 0 || !strings.Contains(diag.String(), "-index-id") || !strings.Contains(diag.String(), "-view") || out.Len() != 0 {
		t.Fatal("help", code, diag.String())
	}
}

func TestStatsAtomicOutputAndFailures(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	dir := t.TempDir()
	target := filepath.Join(dir, "stats.json")
	args := []string{"stats", "--view", "indexes", "--format", "json", "--output", target, input}
	code, out, s := run(t, args...)
	if code != 0 || len(out) != 0 {
		t.Fatal("atomic file", s)
	}
	original, err := os.ReadFile(target)
	if err != nil || !json.Valid(original) {
		t.Fatal(err)
	}
	code, out, s = run(t, args...)
	if code != 1 || len(out) != 0 || !strings.Contains(s.Error, "exists") {
		t.Fatal("existing file", s)
	}
	for _, alias := range []string{input, filepath.Join(dir, "hard.ibd"), filepath.Join(dir, "symlink.ibd")} {
		if alias != input {
			if strings.HasSuffix(alias, "hard.ibd") {
				err = os.Link(input, alias)
			} else {
				err = os.Symlink(input, alias)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		code, _, s = run(t, "stats", "--overwrite", "--output", alias, input)
		if code != 1 || !strings.Contains(s.Error, "input") {
			t.Fatal("input alias", alias, s)
		}
	}
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	x := append([]byte{}, before...)
	x[3*16384+200] ^= 1
	corrupt := filepath.Join(dir, "corrupt.ibd")
	if err = os.WriteFile(corrupt, x, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, s = run(t, "stats", "--view", "levels", "--index-id", "249", "--overwrite", "--output", target, corrupt)
	if code != 1 || len(out) != 0 || !strings.Contains(s.Error, "CRC32C") {
		t.Fatal("filtered full validation", s)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(after, original) {
		t.Fatal("failed publication changed existing target")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".innodb-reader-*"))
	if len(files) != 0 {
		t.Fatal("temp leaked", files)
	}
	for _, flags := range [][]string{{"--max-pages", "6"}, {"--space-max-entries", "1"}} {
		args := append([]string{"stats"}, flags...)
		args = append(args, input)
		code, out, s = run(t, args...)
		if code != 1 || len(out) != 0 || !strings.Contains(s.Error, "resource limit") {
			t.Fatal("limit", s)
		}
	}
	code, _, s = run(t, "stats", filepath.Join(dir, "missing.ibd"))
	if code != 1 {
		t.Fatal("missing input", s)
	}
	code, _, s = run(t, "stats", dir)
	if code != 1 || !strings.Contains(s.Error, "regular") {
		t.Fatal("directory input", s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output, diag bytes.Buffer
	if code = Run(ctx, []string{"stats", input}, &output, &diag); code != 130 || output.Len() != 0 || !strings.Contains(diag.String(), `"complete":false`) {
		t.Fatal("cancelled", code, diag.String())
	}
	code, _, s = run(t, "stats", "--overwrite", "--format", "json", "--output", target, input)
	if code != 0 {
		t.Fatal("overwrite", s)
	}
	after, _ = os.ReadFile(input)
	if !bytes.Equal(after, before) {
		t.Fatal("input changed")
	}
}

type cancellingWriter struct{ cancel context.CancelFunc }

func (w cancellingWriter) Write(b []byte) (int, error) { w.cancel(); return len(b), nil }
func TestStatsOutputErrors(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, format := range []string{"table", "json"} {
		for _, w := range []io.Writer{shortWriter{}, brokenWriter{}} {
			var diag bytes.Buffer
			if code := Run(context.Background(), []string{"stats", "--format", format, input}, w, &diag); code != 1 || !strings.Contains(diag.String(), `"complete":false`) {
				t.Fatal("output failure", format, code, diag.String())
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		var diag bytes.Buffer
		code := Run(ctx, []string{"stats", "--format", format, input}, cancellingWriter{cancel}, &diag)
		cancel()
		if code != 130 || !strings.Contains(diag.String(), `"complete":false`) {
			t.Fatal("write cancellation", format, code, diag.String())
		}
		if code := Run(context.Background(), []string{"stats", "--format", format, input}, io.Discard, shortWriter{}); code != 1 {
			t.Fatal("diagnostic short write", format, code)
		}
	}
}
