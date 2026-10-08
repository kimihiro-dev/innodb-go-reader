package visual

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	innodb "innodb-go-reader"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".gz") {
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		b, err = io.ReadAll(z)
		if err != nil {
			t.Fatal(err)
		}
	}
	return b
}
func embedded(t testing.TB, html string) map[string]any {
	t.Helper()
	prefix := `<script id="report-data" type="application/json">`
	a, b, ok := strings.Cut(html, prefix)
	_ = a
	if !ok {
		t.Fatal("missing data")
	}
	raw, _, ok := strings.Cut(b, "</script>")
	if !ok {
		t.Fatal("unclosed data")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestReportLayers(t *testing.T) {
	for _, path := range []string{"mysql8045/lesson_rows.ibd", "trees/deep_rows.ibd.gz", "variable/external_text.ibd.gz", "generated/mixed_instant.ibd.gz"} {
		t.Run(path, func(t *testing.T) {
			b := fixture(t, path)
			r := bytes.NewReader(b)
			space, err := innodb.AnalyzeSpace(t.Context(), r, int64(len(b)), innodb.SpaceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			plain, err := Build(t.Context(), r, int64(len(b)), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(plain.RawPages) != 0 || len(plain.Details) != 0 || len(plain.TreePages) != 0 {
				t.Fatal("implicit detail")
			}
			if !reflect.DeepEqual(plain.Space, space) {
				t.Fatal("space projection differs")
			}
			meta, err := innodb.InspectTable(r, int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			auto, err := innodb.ReadMaterializedAuto(r, int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			wanted := auto.Result
			selected := map[uint32]bool{wanted.Page.Number: true}
			pages := []uint32{wanted.Page.Number}
			if len(wanted.Records) > 0 && !selected[wanted.Records[0].PageNumber] {
				pages = append(pages, wanted.Records[0].PageNumber)
				selected[wanted.Records[0].PageNumber] = true
			}
			if len(wanted.Records) > 0 && !selected[wanted.Records[len(wanted.Records)-1].PageNumber] {
				pages = append(pages, wanted.Records[len(wanted.Records)-1].PageNumber)
				selected[wanted.Records[len(wanted.Records)-1].PageNumber] = true
			}
			got, err := Build(t.Context(), r, int64(len(b)), Options{Index: meta.Indexes[0].Name, Pages: pages, Materialized: true})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.TreePages, wanted.Pages) || len(got.Edges) != len(wanted.Nodes) {
				t.Fatal("tree projection")
			}
			for i, n := range wanted.Nodes {
				if got.Edges[i] != (Edge{n.PageNumber, n.ChildPage, n.Start, n.Offset, n.End, n.Minimum}) {
					t.Fatal("edge differs", i)
				}
			}
			count := 0
			for _, record := range wanted.Records {
				if !selected[record.PageNumber] {
					continue
				}
				d := got.Details[count]
				count++
				if d.Page != record.PageNumber || d.Start != record.Start || d.Origin != record.Offset || d.End != record.End || !reflect.DeepEqual(d.External, record.External) {
					t.Fatal("record/LOB projection")
				}
				for _, external := range d.External {
					if !bytes.Equal(external.Reference[:], b[int(d.Page)*innodb.PageSize+external.Offset:int(d.Page)*innodb.PageSize+external.Offset+20]) {
						t.Fatal("reference byte location")
					}
				}
			}
			if count != len(got.Details) {
				t.Fatal("unexpected details")
			}
			for _, raw := range got.RawPages {
				if raw.Hex != hex.EncodeToString(b[int(raw.Number)*innodb.PageSize:int(raw.Number+1)*innodb.PageSize]) {
					t.Fatal("raw bytes")
				}
			}
			var html bytes.Buffer
			if err = WriteHTML(t.Context(), &html, got); err != nil {
				t.Fatal(err)
			}
			data := embedded(t, html.String())
			if data["Version"] != "1" || data["Index"] != meta.Indexes[0].Name {
				t.Fatal("embedded values")
			}
			if strings.Contains(html.String(), `"Values":`) {
				t.Fatal("user values retained")
			}
		})
	}
}
func TestSecondaryReport(t *testing.T) {
	for _, c := range []struct{ path, index string }{{"secondary/integer.ibd.gz", "n_idx"}, {"secondary/deep.ibd.gz", "b_idx"}, {"secondary/changes.ibd.gz", "n_idx"}} {
		t.Run(c.path, func(t *testing.T) {
			b := fixture(t, c.path)
			r := bytes.NewReader(b)
			want, err := innodb.ReadSecondaryAuto(r, int64(len(b)), c.index)
			if err != nil {
				t.Fatal(err)
			}
			pages := []uint32{want.Page.Number}
			if len(want.Records) > 0 && want.Records[0].PageNumber != want.Page.Number {
				pages = append(pages, want.Records[0].PageNumber)
			}
			got, err := Build(t.Context(), r, int64(len(b)), Options{Index: c.index, Pages: pages})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.TreePages, want.Pages) || !reflect.DeepEqual(got.SecondaryFields, want.Schema.Fields) || len(got.Edges) != len(want.Nodes) {
				t.Fatal("secondary projection")
			}
			for i, n := range want.Nodes {
				if got.Edges[i] != (Edge{n.PageNumber, n.ChildPage, n.Start, n.Offset, n.End, n.Minimum}) {
					t.Fatal("secondary edge")
				}
			}
			if len(got.Details) == 0 {
				t.Fatal("missing selected records")
			}
		})
	}
}
func TestSpaceOnlyPartitionAndLarge(t *testing.T) {
	for _, path := range []string{"partitions/ranges-p0.partition.gz", "space/grown.space.gz"} {
		b := fixture(t, path)
		got, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{})
		if err != nil {
			t.Fatal(path, err)
		}
		if got.Space.FilePages != uint64(len(b)/innodb.PageSize) {
			t.Fatal("page count")
		}
		if strings.HasPrefix(path, "partitions/") {
			if out, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{Index: "PRIMARY"}); err == nil || out != nil {
				t.Fatal("partition index silently downgraded")
			}
		}
	}
}
func TestReportLimitsAndErrors(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	r := bytes.NewReader(b)
	cases := []Options{{Pages: []uint32{7}}, {Pages: []uint32{4, 4}}, {Pages: make([]uint32, 257)}, {Index: "absent"}, {Materialized: true}, {ManualDir: "relative"}, {Space: innodb.SpaceOptions{MaxPages: 1}}, {MaxReadCalls: 1}, {MaxReportBytes: 1}, {Index: "PRIMARY", Pages: []uint32{4}, MaxDetails: 1}, {Index: "PRIMARY", Scan: innodb.ScanOptions{MaxRows: 1}}, {Index: "PRIMARY", Scan: innodb.ScanOptions{MaxPageReads: 1}}}
	for i, o := range cases {
		if got, err := Build(t.Context(), r, int64(len(b)), o); err == nil || got != nil {
			t.Fatal("bad option", i, err)
		}
	}
	full, err := Build(t.Context(), r, int64(len(b)), Options{Index: "PRIMARY", Pages: []uint32{4}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Build(t.Context(), r, int64(len(b)), Options{Index: "PRIMARY", Pages: []uint32{4}, MaxReadCalls: full.ReadCalls - 1}); !errors.Is(err, innodb.ErrLimit) || got != nil {
		t.Fatal("source budget", err)
	}
	if _, err := Build(t.Context(), r, int64(len(b)), Options{Index: "PRIMARY", Pages: []uint32{4}, MaxReadCalls: full.ReadCalls}); err != nil {
		t.Fatal("exact source budget", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := Build(ctx, r, int64(len(b)), Options{}); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatal("cancel")
	}
	bad := append([]byte(nil), b...)
	bad[4*innodb.PageSize+200] ^= 1
	if got, err := Build(t.Context(), bytes.NewReader(bad), int64(len(bad)), Options{}); !errors.Is(err, innodb.ErrCorrupt) || got != nil {
		t.Fatal("corrupt", err)
	}
	if out, err := Build(t.Context(), brokenReader{}, int64(len(b)), Options{}); !errors.Is(err, io.ErrUnexpectedEOF) || out != nil {
		t.Fatal("short read", err)
	}
	if _, err := Build(nil, r, int64(len(b)), Options{}); err == nil {
		t.Fatal("nil context")
	}
	if _, err := Build(t.Context(), nil, int64(len(b)), Options{}); err == nil {
		t.Fatal("nil reader")
	}
	virtual := fixture(t, "generated/mixed_instant.ibd.gz")
	if _, err := Build(t.Context(), bytes.NewReader(virtual), int64(len(virtual)), Options{Index: "PRIMARY"}); !errors.Is(err, innodb.ErrUnsupported) {
		t.Fatal("virtual", err)
	}
}

type brokenReader struct{}

func (brokenReader) ReadAt(b []byte, o int64) (int, error) { return 0, nil }

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return 0, nil }
func TestHTMLSafety(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	r, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{Title: `</script><script>alert("x")</script>`, Pages: []uint32{4}, ManualDir: filepath.Join(t.TempDir(), `manual #1`)})
	if err != nil {
		t.Fatal(err)
	}
	r.Space.Pages[4].LSN = ^uint64(0)
	r.Space.Pages[4].Index.IndexID = ^uint64(0)
	var out bytes.Buffer
	if err = WriteHTML(t.Context(), &out, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `<script>alert`) || strings.Count(out.String(), "</script>") != 2 || strings.Contains(out.String(), "ZgotmplZ") {
		t.Fatal("HTML injection/link filtering")
	}
	data := embedded(t, out.String())
	p := data["Space"].(map[string]any)["Pages"].([]any)[4].(map[string]any)
	if p["LSN"] != "18446744073709551615" || p["Index"].(map[string]any)["IndexID"] != "18446744073709551615" {
		t.Fatal("integer precision")
	}
	if !strings.Contains(out.String(), "manual%20%231/") {
		t.Fatal("local manual URI")
	}
	if err = WriteHTML(t.Context(), shortWriter{}, r); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short writer", err)
	}
	if err = WriteHTML(t.Context(), nil, r); err == nil {
		t.Fatal("nil writer")
	}
	if err = WriteHTML(t.Context(), io.Discard, nil); err == nil {
		t.Fatal("nil report")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = WriteHTML(ctx, io.Discard, r); !errors.Is(err, context.Canceled) {
		t.Fatal("render cancel")
	}
	r.maxBytes = 1
	if err = WriteHTML(t.Context(), io.Discard, r); !errors.Is(err, innodb.ErrLimit) {
		t.Fatal("render limit", err)
	}
}
func FuzzHTMLReport(f *testing.F) {
	b := fixture(f, "mysql8045/lesson_rows.ibd")
	base, err := Build(context.Background(), bytes.NewReader(b), int64(len(b)), Options{})
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{"hello", "</script><script>x</script>", "\u2028 & < > \""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, title string) {
		if len(title) > 1000 {
			t.Skip()
		}
		r := *base
		r.Title = title
		var out bytes.Buffer
		if err := WriteHTML(t.Context(), &out, &r); err != nil {
			t.Fatal(err)
		}
		embedded(t, out.String())
		if strings.Count(out.String(), "</script>") != 2 {
			t.Fatal("script escape")
		}
	})
}

type cancelLastWriter struct{ cancel context.CancelFunc }

func (w cancelLastWriter) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("</html>")) {
		w.cancel()
	}
	return len(b), nil
}
func TestHTMLLastWriteCancel(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	r, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := WriteHTML(ctx, cancelLastWriter{cancel}, r); !errors.Is(err, context.Canceled) {
		t.Fatal("last write cancellation", err)
	}
}
