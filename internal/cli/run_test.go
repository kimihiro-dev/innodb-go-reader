package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	innodb "innodb-go-reader"
	"innodb-go-reader/rowio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, path string) string {
	t.Helper()
	path = filepath.Join("../../testdata", path)
	if !strings.HasSuffix(path, ".gz") {
		// Identity/overwrite tests must never risk the immutable source asset.
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		copyPath := filepath.Join(t.TempDir(), "table.ibd")
		if err = os.WriteFile(copyPath, b, 0600); err != nil {
			t.Fatal(err)
		}
		return copyPath
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "table.ibd")
	if err = os.WriteFile(out, b, 0600); err != nil {
		t.Fatal(err)
	}
	return out
}
func run(t *testing.T, args ...string) (int, []byte, summary) {
	t.Helper()
	var out, diag bytes.Buffer
	code := Run(context.Background(), args, &out, &diag)
	var s summary
	if err := json.Unmarshal(diag.Bytes(), &s); err != nil {
		t.Fatalf("diagnostic %q: %v", diag.String(), err)
	}
	if s.Complete != (code == 0) {
		t.Fatal("completion/exit disagreement")
	}
	return code, out.Bytes(), s
}
func readRows(t *testing.T, b []byte, format string) (rowio.Header, []rowio.Row) {
	t.Helper()
	d, err := rowio.NewDecoder(bytes.NewReader(b), format, rowio.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := []rowio.Row{}
	for {
		r, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	if d.Complete() != (format == "jsonl") {
		t.Fatal("completion")
	}
	return d.Header(), rows
}
func TestExportFixtures(t *testing.T) {
	for _, path := range []string{"mysql8045/lesson_rows.ibd", "json/json_opaque.ibd.gz", "json/json_boundaries.ibd.gz", "geometry/geometry_lesson.ibd.gz", "generated/mixed_instant.ibd.gz", "secondary_query/covering.ibd.gz"} {
		t.Run(path, func(t *testing.T) {
			input := fixture(t, path)
			f, err := os.Open(input)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			st, _ := f.Stat()
			want, err := innodb.ReadMaterializedAuto(f, st.Size())
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"jsonl", "csv"} {
				code, b, s := run(t, "export", "--materialized", "--physical", "--format", format, input)
				if code != 0 {
					t.Fatal(s.Error)
				}
				h, rows := readRows(t, b, format)
				if !reflect.DeepEqual(h.Columns, want.Columns) || !reflect.DeepEqual(h.VirtualColumns, want.VirtualColumns) || len(rows) != len(want.Result.Records) {
					t.Fatal("schema/count")
				}
				for i, r := range rows {
					if !reflect.DeepEqual(r.Values, want.Result.Records[i].Values) {
						t.Fatalf("row %d", i)
					}
					var physical innodb.Record
					if err := json.Unmarshal(r.Physical, &physical); err != nil {
						t.Fatal(err)
					}
					if physical.PageNumber != want.Result.Records[i].PageNumber || physical.Offset != want.Result.Records[i].Offset {
						t.Fatal("physical")
					}
				}
			}
		})
	}
}
func TestCommands(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	f, _ := os.Open(input)
	defer f.Close()
	st, _ := f.Stat()
	meta, err := innodb.InspectTable(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	space, err := innodb.AnalyzeSpace(context.Background(), f, st.Size(), innodb.SpaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want any
	}{{[]string{"metadata", input}, meta}, {[]string{"space", input}, space}, {[]string{"page", "--number", "0", input}, space.Pages[0]}, {[]string{"check", "--scope", "space", input}, space}} {
		code, b, s := run(t, test.args...)
		if code != 0 {
			t.Fatal(s.Error)
		}
		want, _ := json.Marshal(test.want)
		if !bytes.Equal(bytes.TrimSpace(b), want) {
			t.Fatal("report differs", test.args)
		}
	}
	code, b, s := run(t, "check", input)
	if code != 0 {
		t.Fatal(s.Error)
	}
	var report innodb.ScanReport
	if err = json.Unmarshal(b, &report); err != nil || !report.Complete || report.Records != 4 {
		t.Fatal(report, err)
	}
	generated := fixture(t, "generated/mixed_instant.ibd.gz")
	if code, _, _ := run(t, "export", generated); code != 1 {
		t.Fatal("strict virtual accepted")
	}
	secondary := fixture(t, "secondary_query/covering.ibd.gz")
	sf, _ := os.Open(secondary)
	defer sf.Close()
	ss, _ := sf.Stat()
	sm, _ := innodb.InspectTable(sf, ss.Size())
	name := ""
	for _, idx := range sm.Indexes {
		if !idx.Clustered {
			name = idx.Name
			break
		}
	}
	code, b, s = run(t, "check", "--scope", "secondary", "--index", name, secondary)
	if code != 0 {
		t.Fatal(s.Error)
	}
	var sr innodb.SecondaryScanReport
	if err = json.Unmarshal(b, &sr); err != nil || !sr.Complete {
		t.Fatal(err)
	}
}
func spec(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "query.json")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestQueries(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	q := spec(t, `{"Lower":{"Key":[2],"Inclusive":true},"Upper":{"Key":[12],"Inclusive":false},"Reverse":true,"Limit":1}`)
	code, b, s := run(t, "query", "--query", q, input)
	if code != 0 {
		t.Fatal(s.Error)
	}
	_, rows := readRows(t, b, "jsonl")
	if len(rows) != 1 || rows[0].Values[0] != int32(7) {
		t.Fatal(rows)
	}
	input = fixture(t, "secondary_query/covering.ibd.gz")
	f, _ := os.Open(input)
	defer f.Close()
	st, _ := f.Stat()
	m, _ := innodb.InspectTable(f, st.Size())
	name := ""
	for _, idx := range m.Indexes {
		if !idx.Clustered {
			name = idx.Name
			break
		}
	}
	query := innodb.SecondaryQuery{Range: innodb.KeyRange{Reverse: true, Limit: 5}, Columns: []string{m.MaterializedSchema.Columns[0].Name}}
	qb, _ := json.Marshal(query)
	path := spec(t, string(qb))
	want := []innodb.ProjectedRow{}
	_, err := innodb.QuerySecondaryAuto(context.Background(), f, st.Size(), name, query, innodb.ScanOptions{}, func(r innodb.ProjectedRow) error { want = append(want, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	code, b, s = run(t, "query", "--query", path, "--index", name, "--physical", input)
	if code != 0 {
		t.Fatal(s.Error)
	}
	h, rows := readRows(t, b, "jsonl")
	if len(h.Columns) != 1 || len(rows) != len(want) {
		t.Fatal("projection/count")
	}
	for i, r := range rows {
		if !reflect.DeepEqual(r.Values, want[i].Values) {
			t.Fatal("secondary values")
		}
		var p innodb.ProjectedRow
		d := json.NewDecoder(bytes.NewReader(r.Physical))
		d.UseNumber()
		if err = d.Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Covered != want[i].Covered || p.SecondaryPage != want[i].SecondaryPage {
			t.Fatal("secondary provenance")
		}
	}
	for _, text := range []string{`null`, `{} {}`, `{"Bogus":1}`, `{"Prefix":[{"base64":"!!"}]}`} {
		code, _, _ := run(t, "query", "--query", spec(t, text), input)
		if code != 2 {
			t.Fatal("invalid query", text)
		}
	}
}
func TestAtomicAndFailure(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.jsonl")
	code, b, s := run(t, "export", "--max-rows", "1", input)
	if code != 1 || s.Complete || !bytes.Contains(b, []byte(`"kind":"row"`)) || bytes.Contains(b, []byte(`"kind":"end"`)) {
		t.Fatal("stream failure", s)
	}
	if code, _, _ := run(t, "export", "--max-rows", "1", "--output", dest, input); code != 1 {
		t.Fatal("failure exit")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("failed output exists")
	}
	os.WriteFile(dest, []byte("original"), 0600)
	for _, args := range [][]string{{"export", "--output", dest, input}, {"export", "--overwrite", "--max-rows", "1", "--output", dest, input}} {
		if code, _, _ := run(t, args...); code != 1 {
			t.Fatal("expected failure")
		}
		b, _ := os.ReadFile(dest)
		if string(b) != "original" {
			t.Fatal("old target changed")
		}
	}
	if code, b, s := run(t, "export", "--overwrite", "--output", dest, input); code != 0 || len(b) != 0 {
		t.Fatal(s)
	}
	b, _ = os.ReadFile(dest)
	readRows(t, b, "jsonl")
	data, _ := os.ReadFile(input)
	for _, alias := range []string{input, filepath.Join(dir, "hard.ibd"), filepath.Join(dir, "sym.ibd")} {
		if alias != input {
			var err error
			if strings.Contains(alias, "hard") {
				err = os.Link(input, alias)
			} else {
				err = os.Symlink(mustAbs(t, input), alias)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if code, _, _ := run(t, "export", "--overwrite", "--output", alias, input); code != 1 {
			t.Fatal("input alias accepted")
		}
		after, _ := os.ReadFile(input)
		if !bytes.Equal(after, data) {
			t.Fatal("input changed")
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".innodb-reader-*"))
	if len(files) != 0 {
		t.Fatal("temporary files remain")
	}
	q := spec(t, `{}`)
	if code, _, _ := run(t, "query", "--query", q, "--output", q, "--overwrite", input); code != 1 {
		t.Fatal("query overwrite")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diag bytes.Buffer
	if code := Run(ctx, []string{"export", "--output", dest, "--overwrite", input}, &out, &diag); code != 130 {
		t.Fatal("cancel", code)
	}
	for _, args := range [][]string{{"export", "--max-page-reads", "1", input}, {"space", "--max-pages", "1", input}, {"page", "--number", "9999", input}} {
		if code, _, _ := run(t, args...); code != 1 {
			t.Fatal("budget/page accepted")
		}
	}
}
func mustAbs(t *testing.T, path string) string {
	t.Helper()
	v, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestUsageAndIO(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, args := range [][]string{nil, {"wat"}, {"export", "--format", "plain", input}, {"page", input}, {"query", input}, {"export", "--overwrite", input}, {"check", "--scope", "space", "--max-rows", "1", input}, {"check", "--scope", "secondary", input}} {
		code, _, _ := run(t, args...)
		if code != 2 {
			t.Fatal(args, code)
		}
	}
	var out, diag bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--unknown", input}, &out, &diag); code != 2 || out.Len() != 0 {
		t.Fatal("unknown flag")
	}
	diag.Reset()
	if code := Run(context.Background(), []string{"export", input}, brokenWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), `"complete":false`) {
		t.Fatal("write error")
	}
	if code := Run(context.Background(), []string{"metadata", input}, io.Discard, brokenWriter{}); code != 1 {
		t.Fatal("diagnostic failure")
	}
	for _, args := range [][]string{{"--help"}, {"export", "--help"}} {
		out.Reset()
		diag.Reset()
		if code := Run(context.Background(), args, &out, &diag); code != 0 || out.Len() != 0 || !strings.Contains(diag.String(), "usage:") {
			t.Fatal("help")
		}
	}
}

func TestSubprocess(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "innodb-reader")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/innodb-reader")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v", b, err)
	}
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, test := range []struct {
		args []string
		code int
	}{{[]string{"--help"}, 0}, {[]string{"bad"}, 2}, {[]string{"export", input}, 0}, {[]string{"export", "--max-rows", "1", input}, 1}} {
		cmd := exec.Command(binary, test.args...)
		var out, diag bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diag
		err := cmd.Run()
		code := 0
		if err != nil {
			var x *exec.ExitError
			if !errors.As(err, &x) {
				t.Fatal(err)
			}
			code = x.ExitCode()
		}
		if code != test.code {
			t.Fatalf("%v: exit %d %s", test.args, code, diag.String())
		}
	}
	// Wait for the first streamed byte, then cancel while stdout is drained. This
	// synchronizes on actual work rather than racing process startup with sleep.
	large := fixture(t, "trees/deep_rows.ibd.gz")
	cmd := exec.Command(binary, "export", large)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diag bytes.Buffer
	cmd.Stderr = &diag
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ready := make(chan error, 1)
	go func() { b := make([]byte, 1); _, e := io.ReadFull(out, b); ready <- e }()
	select {
	case err = <-ready:
		if err != nil {
			t.Fatal(err, diag.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no subprocess output")
	}
	if err = cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, out)
	err = cmd.Wait()
	var x *exec.ExitError
	if !errors.As(err, &x) || x.ExitCode() != 130 {
		t.Fatalf("SIGINT: %v %s", err, diag.String())
	}
	if !strings.Contains(diag.String(), `"complete":false`) {
		t.Fatal("cancel success report")
	}

	// Closing the real pipe exercises main's SIGPIPE handling, not just a fake
	// failing writer passed to Run.
	pipeCmd := exec.Command(binary, "export", large)
	pipe, err := pipeCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var pipeDiag bytes.Buffer
	pipeCmd.Stderr = &pipeDiag
	if err = pipeCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pipeCmd.Process.Kill()
	first := make([]byte, 1)
	if _, err = io.ReadFull(pipe, first); err != nil {
		t.Fatal(err)
	}
	pipe.Close()
	err = pipeCmd.Wait()
	var pipeExit *exec.ExitError
	if !errors.As(err, &pipeExit) || pipeExit.ExitCode() != 1 || !strings.Contains(pipeDiag.String(), `"complete":false`) {
		t.Fatalf("broken pipe: %v %s", err, pipeDiag.String())
	}
}

func TestLateCorruptionAndAtomicCancellation(t *testing.T) {
	input := fixture(t, "trees/deep_rows.ibd.gz")
	b, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := innodb.ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	last := result.Records[len(result.Records)-1].PageNumber
	b[int(last)*innodb.PageSize+1000] ^= 1
	corrupt := filepath.Join(t.TempDir(), "corrupt.ibd")
	if err = os.WriteFile(corrupt, b, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, s := run(t, "export", corrupt)
	if code != 1 || !strings.Contains(s.Error, "CRC32C") || !bytes.Contains(out, []byte(`"kind":"row"`)) || bytes.Contains(out, []byte(`"kind":"end"`)) {
		t.Fatalf("late corruption: %d %s", code, s.Error)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "out")
	os.WriteFile(target, []byte("old"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	err = output(ctx, target, true, nil, io.Discard, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial"); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(target)
	if string(after) != "old" {
		t.Fatal("cancel changed original")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".innodb-reader-*"))
	if len(files) != 0 {
		t.Fatal("cancel left temp")
	}
	// A destination appearing between validation and publication cannot be
	// overwritten by the default no-replace path.
	dest := filepath.Join(dir, "racing")
	err = output(context.Background(), dest, false, nil, io.Discard, func(w io.Writer) error {
		if err := os.WriteFile(dest, []byte("other"), 0600); err != nil {
			return err
		}
		_, err := io.WriteString(w, "ours")
		return err
	})
	if err == nil {
		t.Fatal("publication overwrote racing destination")
	}
	after, _ = os.ReadFile(dest)
	if string(after) != "other" {
		t.Fatal("racing destination changed")
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestShortWrites(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, command := range []string{"metadata", "space", "export"} {
		var diag bytes.Buffer
		if Run(context.Background(), []string{command, input}, shortWriter{}, &diag) != 1 {
			t.Fatal(command, "short output accepted")
		}
	}
	if Run(context.Background(), []string{"metadata", input}, io.Discard, shortWriter{}) != 1 {
		t.Fatal("short diagnostic accepted")
	}
	if Run(context.Background(), []string{"export", "--help"}, io.Discard, brokenWriter{}) != 1 {
		t.Fatal("help diagnostic error ignored")
	}
}

func TestExactQueryNumbersAndBinary(t *testing.T) {
	q, err := readQuery(spec(t, `{"Prefix":[18446744073709551615,{"base64":""},{"base64":"AP8="}]}`), false)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := q.Range.Prefix[0].(json.Number)
	if !ok || n.String() != "18446744073709551615" {
		t.Fatal("integer precision")
	}
	if !reflect.DeepEqual(q.Range.Prefix[1], []byte{}) || !reflect.DeepEqual(q.Range.Prefix[2], []byte{0, 255}) {
		t.Fatal("binary key/empty")
	}
}

func TestQueryIOAndTextErrors(t *testing.T) {
	input := fixture(t, "mysql8045/lesson_rows.ibd")
	if code, _, _ := run(t, "query", "--query", filepath.Join(t.TempDir(), "missing.json"), input); code != 1 {
		t.Fatal("query I/O should be runtime error")
	}
	if code, _, _ := run(t, "query", "--query", spec(t, "{\"Prefix\":[\""+string([]byte{255})+"\"]}"), input); code != 2 {
		t.Fatal("invalid UTF-8 query accepted")
	}
}
