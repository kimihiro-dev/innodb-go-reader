package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDiagnoseLesson(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || !p.Passed || len(p.Problems) != 0 {
		t.Fatal(p, err)
	}
	b[4*PageSize+176] ^= 1 // Bad CRC; never resealed.
	p, err = Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 {
		t.Fatal(p, err)
	}
	issue := p.Problems[0]
	if issue.Code != ProblemChecksum || !errors.Is(issue, ErrCorrupt) || issue.Location.Page == nil || *issue.Location.Page != 4 || issue.Location.Bytes == nil || issue.Expected == "" || issue.Actual == "" {
		t.Fatal(issue)
	}
}

func diagnosticCheck(t testing.TB, p DiagnosticReport, layer string, id uint64) DiagnosticCheck {
	t.Helper()
	for _, c := range p.Checks {
		if c.Layer == layer && (c.IndexID == nil || *c.IndexID == id) {
			return c
		}
	}
	t.Fatalf("missing %s check for %d", layer, id)
	return DiagnosticCheck{}
}

func TestDiagnoseIndependentPagesAndLimits(t *testing.T) {
	b, s := treeFixture(t, "deep_rows")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	first, last := full.Records[0].PageNumber, full.Records[len(full.Records)-1].PageNumber
	if first == last {
		t.Fatal("need independent leaf pages")
	}
	x := append([]byte{}, b...)
	for _, n := range []uint32{first, last} {
		x[int(n)*PageSize+200] ^= 1 // Actual bad CRC, never resealed.
	}
	p, err := Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || len(p.Problems) != 2 {
		t.Fatal("independent damaged pages", p, err)
	}
	seen := map[uint32]bool{}
	for _, issue := range p.Problems {
		if issue.Code != ProblemChecksum || !errors.Is(issue, ErrCorrupt) || issue.Location.Page == nil {
			t.Fatal(issue)
		}
		seen[*issue.Location.Page] = true
	}
	if !seen[first] || !seen[last] || diagnosticCheck(t, p, "allocation", 0).State != CheckBlocked || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckBlocked {
		t.Fatal("dependent checks must be blocked", p)
	}
	p, err = Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{MaxIssues: 1})
	if !errors.Is(err, ErrLimit) || p.Complete || p.Passed || !p.Truncated || len(p.Problems) != 1 {
		t.Fatal("problem cap", p, err)
	}
	// Root record layout is synthetically damaged inside an otherwise valid envelope.
	x = append([]byte{}, b...)
	node := full.Nodes[0]
	be.PutUint32(x[int(s.RootPage)*PageSize+node.End-4:], 0)
	resealTestPages(x)
	p, err = Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckFailed || len(p.Problems) != 1 || p.Problems[0].Code != ProblemNavigation || *p.Problems[0].Location.Page != s.RootPage {
		t.Fatal("root navigation failure", p, err)
	}
}

func TestDiagnoseAllocationDependencies(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	for _, c := range []struct {
		name  string
		edit  func([]byte)
		span  ByteSpan
		pages CheckState
	}{
		{"descriptor", func(x []byte) { be.PutUint32(x[170:], 0) }, ByteSpan{170, 174}, CheckBlocked},
		{"fragment-count", func(x []byte) { be.PutUint32(x[58:], 999) }, ByteSpan{58, 62}, CheckPassed},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := append([]byte{}, b...)
			c.edit(x)
			resealTestPages(x) // Synthetic allocation metadata, not repaired evidence.
			p, err := Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{})
			if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 {
				t.Fatal(p, err)
			}
			issue := p.Problems[0]
			if issue.Code != ProblemAllocation || issue.Location.Page == nil || *issue.Location.Page != 0 || issue.Location.Bytes == nil || *issue.Location.Bytes != c.span || diagnosticCheck(t, p, "pages", 0).State != c.pages || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckBlocked {
				t.Fatal("allocation dependency/location", p)
			}
			if out, err := AnalyzeSpace(t.Context(), bytes.NewReader(x), int64(len(x)), SpaceOptions{}); out != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("strict allocation weakened", err)
			}
		})
	}
	space := spaceResult(t, b)
	free := false
	for _, page := range space.Pages {
		if page.Allocation == "free" {
			b[int(page.Number)*PageSize+200] = 255 // Free residue has no valid CRC.
			free = true
			break
		}
	}
	p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if !free || err != nil || !p.Complete || !p.Passed || p.FilePages != p.UsedPages+p.SkippedPages {
		t.Fatal("free residue misclassified", p, err)
	}
}

func TestDiagnoseRejectsFreeRootResidue(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	space := spaceResult(t, b)
	for _, page := range space.Pages {
		if page.Allocation != "free" {
			continue
		}
		// A synthetic valid-looking root of a different index in a free page.
		// The allocated original tree remains untouched and valid.
		fakeIndex := s.IndexID + 1000
		x := append([]byte{}, b...)
		copy(x[int(page.Number)*PageSize:int(page.Number+1)*PageSize], b[int(s.RootPage)*PageSize:int(s.RootPage+1)*PageSize])
		be.PutUint32(x[int(page.Number)*PageSize+4:], page.Number)
		be.PutUint64(x[int(page.Number)*PageSize+66:], fakeIndex)
		resealTestPages(x)
		s.RootPage, s.IndexID = page.Number, fakeIndex
		if err := ValidateSchema(bytes.NewReader(x), int64(len(x)), s); err != nil {
			t.Fatal("fixture must pass identity-only precheck", err)
		}
		p, err := Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{})
		if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 || p.Problems[0].Code != ProblemCoverage || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckBlocked || diagnosticCheck(t, p, "allocation", 0).State != CheckPassed {
			t.Fatal("free residue certified as current root", p, err)
		}
		return
	}
	t.Fatal("no free page for root residue test")
}

func TestProblemFieldCoordinates(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	b[4*PageSize+176] = 255
	resealTestPages(b) // Synthetic invalid UTF-8 with a valid page envelope.
	out, err := Read(bytes.NewReader(b), int64(len(b)), s)
	var problem *Problem
	if out != nil || !errors.Is(err, ErrCorrupt) || !errors.As(err, &problem) {
		t.Fatal("strict error chain", out, err)
	}
	l := problem.Location
	if problem.Code != ProblemFieldValue || l.Page == nil || *l.Page != 4 || l.RecordPage == nil || *l.RecordPage != 4 || l.RecordOffset == nil || *l.RecordOffset != 155 || l.Column == nil || *l.Column != 2 || l.ColumnName != "name" || l.Bytes == nil || *l.Bytes != (ByteSpan{176, 182}) {
		t.Fatal("independent lesson byte positions", problem)
	}
	p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 || diagnosticCheck(t, p, "values", s.IndexID).State != CheckFailed || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckBlocked || diagnosticCheck(t, p, "allocation", 0).State != CheckPassed {
		t.Fatal("field failure layering", p, err)
	}
	raw, err := json.Marshal(p)
	if err != nil || bytes.Contains(raw, []byte("Cause")) || !bytes.Contains(raw, []byte("field.value")) {
		t.Fatal("diagnostic JSON", string(raw), err)
	}
	if len(byteObservation(make([]byte, 10000))) > 180 {
		t.Fatal("unbounded observation")
	}
}

func TestProblemInstantLogicalColumn(t *testing.T) {
	b, s := instantFixture(t, "lesson_readd")
	page, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range page.Sources {
		for _, f := range source.Fields {
			if f.Kind != "stored" || f.Column != 0 || f.Null || f.Data == nil || f.Data.Start == f.Data.End {
				continue
			}
			b[int(s.RootPage)*PageSize+f.Data.Start] = 255
			resealTestPages(b) // Synthetic invalid re-added VARCHAR value.
			p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{SkipLOB: true})
			if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 {
				t.Fatal(p, err)
			}
			issue := p.Problems[0]
			if issue.Code != ProblemFieldValue || issue.Location.Column == nil || *issue.Location.Column != 0 || issue.Location.ColumnName != "a" || *issue.Location.RecordOffset != source.Offset || *issue.Location.Bytes != *f.Data {
				t.Fatal("physical INSTANT ordinal leaked as current column", issue)
			}
			return
		}
	}
	t.Fatal("missing stored re-added column")
}

type diagnosticIOReader struct {
	data   []byte
	fail   map[uint32]error
	seen   []uint32
	cancel context.CancelFunc
}

func (r *diagnosticIOReader) ReadAt(b []byte, off int64) (int, error) {
	n := uint32(off / PageSize)
	r.seen = append(r.seen, n)
	if r.cancel != nil && len(r.seen) == 2 {
		r.cancel()
	}
	if err := r.fail[n]; err != nil {
		return 0, err
	}
	return bytes.NewReader(r.data).ReadAt(b, off)
}

type diagnosticFullEOF struct{ io.ReaderAt }

func (r diagnosticFullEOF) ReadAt(b []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(b, off)
	if err == nil && n == len(b) {
		err = io.EOF
	}
	return n, err
}

func TestDiagnoseControl(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	size := int64(len(b))
	for _, options := range []DiagnosticOptions{{MaxPages: 1}, {ScanOptions: ScanOptions{MaxPageReads: 1}}, {ScanOptions: ScanOptions{MaxEntries: 1}}, {ScanOptions: ScanOptions{MaxRows: 1}}, {ScanOptions: ScanOptions{MaxRowBytes: 1}}} {
		p, err := Diagnose(t.Context(), bytes.NewReader(b), size, s, options)
		if !errors.Is(err, ErrLimit) || p.Complete || p.Passed || !p.Truncated {
			t.Fatal(options, p, err)
		}
	}
	cause := errors.New("disk unavailable")
	r := &diagnosticIOReader{data: b, fail: map[uint32]error{3: cause, 4: io.ErrClosedPipe}}
	p, err := Diagnose(t.Context(), r, size, s, DiagnosticOptions{})
	if !errors.Is(err, cause) || !errors.Is(err, io.ErrClosedPipe) || p.Complete || p.Passed || p.Truncated || len(p.Problems) != 2 {
		t.Fatal("independent I/O and original causes", p, err)
	}
	if len(r.seen) < 5 || p.PageReads != p.CacheHits+p.PhysicalReads {
		t.Fatal("independent pages/shared counters", r.seen, p)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = &diagnosticIOReader{data: b, cancel: cancel}
	p, err = Diagnose(ctx, r, size, s, DiagnosticOptions{})
	if !errors.Is(err, context.Canceled) || p.Complete || p.Passed || p.Truncated {
		t.Fatal("cancellation", p, err)
	}
	p, err = Diagnose(t.Context(), bytes.NewReader(b), size-1, s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || p.Problems[0].Code != ProblemGeometry || p.PageReads != 0 {
		t.Fatal("invalid geometry is a finding", p, err)
	}
	if p, err := Diagnose(nil, bytes.NewReader(b), size, s, DiagnosticOptions{}); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("invalid API input", p, err)
	}
	p, err = Diagnose(t.Context(), diagnosticFullEOF{bytes.NewReader(b)}, size, s, DiagnosticOptions{})
	if err != nil || !p.Complete || !p.Passed {
		t.Fatal("complete ReaderAt page with EOF", p, err)
	}
}

func TestDiagnoseLOBAndUnsupported(t *testing.T) {
	b, s, _ := secondaryQueryFixture(t, "secondary_query", "huge", "s")
	p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{SkipLOB: true})
	if err != nil || !p.Complete || !p.Passed || diagnosticCheck(t, p, "lob", s.IndexID).State != CheckNotRequested {
		t.Fatal("explicit local value scope", p, err)
	}
	p, err = Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 || !errors.Is(p.Problems[0], ErrUnsupported) || diagnosticCheck(t, p, "lob", s.IndexID).State != CheckUnsupported {
		t.Fatal("typed value cap remains unsupported", p, err)
	}
	b, s = compactFixture(t, "testdata/compact", "lesson_compact_initial")
	local, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, record := range local.Records {
		for _, field := range record.External {
			if changed == 2 {
				break
			}
			be.PutUint32(b[int(record.PageNumber)*PageSize+field.Offset+4:], uint32(len(b)/PageSize+changed+1))
			changed++
		}
	}
	resealTestPages(b) // Synthetic dangling LOB references; allocation remains valid.
	p, err = Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
	if changed != 2 || err != nil || !p.Complete || p.Passed || len(p.Problems) != 2 || diagnosticCheck(t, p, "allocation", 0).State != CheckPassed {
		t.Fatal("independent dangling LOBs", p, err)
	}
	for _, issue := range p.Problems {
		l := issue.Location
		if l.Page == nil || *l.Page < uint32(len(b)/PageSize) || l.RecordPage == nil || *l.RecordPage != s.RootPage || l.RecordOffset == nil || l.Column == nil || l.Bytes != nil || !errors.Is(issue, ErrCorrupt) {
			t.Fatal("LOB physical page vs owning record", issue)
		}
	}
}

func TestDiagnoseFixtures(t *testing.T) {
	for _, c := range []struct{ dir, name string }{
		{"testdata/compact", "lesson_compact_null_deleted"},
		{"testdata/cluster", "rowid_lesson"},
		{"testdata/generated", "mixed_instant"},
		{"testdata/instant", "lesson_readd"},
		{"testdata/mysql5744", "lob_compact_initial"},
		{"testdata/mysql5744", "scalar_dynamic_initial"},
		{"testdata/mysql80-patches/8.0.28", "instant_compact_mixed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b []byte
			var s Schema
			if strings.Contains(c.dir, "mysql80-patches") {
				b = unzip(t, c.dir+"/"+c.name+".ibd.gz")
				m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
				if err != nil || m.MaterializedSchema == nil {
					t.Fatal(err)
				}
				s = *m.MaterializedSchema
			} else {
				b, s = compactFixture(t, c.dir, c.name)
			}
			p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{})
			if err != nil || !p.Complete || !p.Passed || len(p.Problems) != 0 || p.FilePages != p.UsedPages+p.SkippedPages || len(p.Unverified) == 0 {
				t.Fatal(p, err)
			}
		})
	}
	b, s, indexes := mysql57SecondaryFixture(t, "ordinary_compact")
	p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{Secondary: indexes})
	if err != nil || !p.Complete || !p.Passed {
		t.Fatal("secondary scopes", p, err)
	}
	for _, index := range indexes {
		if c := diagnosticCheck(t, p, "tree", index.IndexID); c.State != CheckPassed || c.Checked == 0 {
			t.Fatal(c)
		}
	}
	bad := s
	bad.Columns = append([]Column{}, s.Columns...)
	bad.Columns[0].Type = "unsupported"
	p, err = Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), bad, DiagnosticOptions{})
	if err != nil || !p.Complete || p.Passed || p.Problems[0].Code != ProblemSchema || !errors.Is(p.Problems[0], ErrUnsupported) || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckBlocked {
		t.Fatal("unsupported definition", p, err)
	}
}

func TestDiagnoseSecondaryIndependentScope(t *testing.T) {
	b, s, index := secondaryQueryFixture(t, "secondary_query", "prefixes", "s")
	page, err := TraceSecondaryPage(bytes.NewReader(b), int64(len(b)), index.RootPage, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range page.Sources {
		for _, field := range source.Fields {
			if field.Column < 0 || !s.Columns[field.Column].isText() || field.Null || field.Data == nil || field.Data.Start == field.Data.End {
				continue
			}
			b[int(index.RootPage)*PageSize+field.Data.Start] = 255
			resealTestPages(b) // Invalid secondary text in a disposable valid envelope.
			p, err := Diagnose(t.Context(), bytes.NewReader(b), int64(len(b)), s, DiagnosticOptions{SkipLOB: true, Secondary: []SecondarySchema{index}})
			if err != nil || !p.Complete || p.Passed || len(p.Problems) != 1 || diagnosticCheck(t, p, "tree", s.IndexID).State != CheckPassed || diagnosticCheck(t, p, "tree", index.IndexID).State != CheckBlocked {
				t.Fatal("independent selected index", p, err)
			}
			issue := p.Problems[0]
			if issue.Code != ProblemFieldValue || issue.Location.Column == nil || *issue.Location.Column != field.Column || *issue.Location.Page != index.RootPage || *issue.Location.RecordOffset != source.Offset || *issue.Location.Bytes != *field.Data {
				t.Fatal("secondary source column mapping", issue)
			}
			return
		}
	}
	t.Fatal("no secondary text field")
}

func FuzzDiagnose(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(uint32(4*PageSize+176), byte(255), true)
	f.Add(uint32(170), byte(7), true)
	f.Add(uint32(4*PageSize+200), byte(1), false)
	f.Fuzz(func(t *testing.T, offset uint32, value byte, reseal bool) {
		x := append([]byte{}, b...)
		x[int(offset)%len(x)] ^= value
		if reseal {
			resealTestPages(x) // Synthetic structure; never write the fixed source.
		}
		p, err := Diagnose(t.Context(), bytes.NewReader(x), int64(len(x)), s, DiagnosticOptions{MaxPages: 100, MaxIssues: 10, ScanOptions: ScanOptions{MaxPageReads: 100, MaxEntries: 3000, MaxRows: 100}})
		if p.Passed && (!p.Complete || err != nil || len(p.Problems) != 0) || err != nil && p.Complete || len(p.Problems) > 10 || p.PageReads > 100 || p.TraversalEntries > 3000 {
			t.Fatal("diagnostic completion/budget invariant", p, err)
		}
		for _, issue := range p.Problems {
			if issue.Code == "" || issue.Expected == "" || issue.Actual == "" || issue.Cause == nil {
				t.Fatal("incomplete structured finding", issue)
			}
			if span := issue.Location.Bytes; span != nil && (span.Start < 0 || span.End < span.Start || span.End > PageSize) {
				t.Fatal("invalid physical source range", issue)
			}
		}
	})
}
