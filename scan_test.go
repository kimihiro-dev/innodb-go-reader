package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func scanFixture(t testing.TB, path string) []byte {
	t.Helper()
	if strings.HasSuffix(path, ".gz") {
		return unzip(t, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func collectScan(result *Result) func(ScanEvent) error {
	return func(e ScanEvent) error {
		switch {
		case e.Page != nil:
			if len(result.Pages) == 0 {
				result.Page = *e.Page
			}
			result.Pages = append(result.Pages, *e.Page)
		case e.Node != nil:
			result.Nodes = append(result.Nodes, *e.Node)
		case e.Record != nil:
			result.Records = append(result.Records, *e.Record)
		case e.DeletedRecord != nil:
			result.DeletedRecords = append(result.DeletedRecords, *e.DeletedRecord)
		default:
			return errors.New("empty event")
		}
		return nil
	}
}

func TestScanAllFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/*/*.ibd*")
	if err != nil {
		t.Fatal(err)
	}
	successful, rejected, rows, external := 0, 0, 0, 0
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			b := scanFixture(t, path)
			r := bytes.NewReader(b)
			size := int64(len(b))
			want, werr := ReadMaterializedAuto(r, size)
			got := &Result{Records: make([]Record, 0)}
			report, err := ScanMaterializedAuto(context.Background(), r, size, ScanOptions{}, collectScan(got))
			if werr != nil {
				if err == nil || report.Complete {
					t.Fatal("accepted rejected input", err)
				}
				for _, sentinel := range []error{ErrCorrupt, ErrUnsupported} {
					if errors.Is(werr, sentinel) && !errors.Is(err, sentinel) {
						t.Fatal("error class", werr, err)
					}
				}
				rejected++
				return
			}
			if err != nil || !report.Complete {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want.Result) || !reflect.DeepEqual(report.Columns, want.Columns) || !reflect.DeepEqual(report.VirtualColumns, want.VirtualColumns) {
				t.Fatal("stream/atomic result")
			}
			if report.Records != uint64(len(got.Records)) || report.Pages != uint64(len(got.Pages)) || report.Nodes != uint64(len(got.Nodes)) || report.DeletedRecords != uint64(len(got.DeletedRecords)) {
				t.Fatal("event counters")
			}
			if report.PageReads != report.PhysicalReads+report.CacheHits {
				t.Fatal("I/O counters")
			}
			// Stream each external value independently; chunks and concatenated bytes must
			// match the atomic reader, including old chains and updated LOB history.
			for _, record := range want.Result.Records {
				for _, field := range record.External {
					var data []byte
					var chunks []LOBChunk
					lr, err := StreamLOB(context.Background(), r, size, field, ScanOptions{}, func(block LOBBlock) error {
						if block.Offset != uint64(len(data)) {
							return errors.New("block offset")
						}
						data = append(data, block.Data...)
						if !block.Prefix {
							chunks = append(chunks, block.Chunk)
						}
						return nil
					})
					if err != nil || !lr.Complete || lr.Bytes != uint64(field.Length)+uint64(len(field.Prefix)) || !reflect.DeepEqual(chunks, field.Chunks) {
						t.Fatal("LOB stream", err)
					}
					value, err := variableValue(want.Columns[field.Column], data)
					if want.Columns[field.Column].Type == "CHAR" {
						value = strings.TrimRight(value.(string), " ")
					}
					if err != nil || !reflect.DeepEqual(value, record.Values[field.Column]) {
						t.Fatal("LOB value", err)
					}
					external++
				}
			}
			successful++
			rows += len(got.Records)
		})
	}
	if successful != 484 || rejected != 3 || rows != 98811 {
		t.Fatalf("files=%d rejected=%d rows=%d", successful, rejected, rows)
	}
	t.Logf("%d successful, %d rejected, %d rows, %d external values", successful, rejected, rows, external)
}

func TestScanControl(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "tree_compact_instant")
	r := bytes.NewReader(b)
	size := int64(len(b))
	ctx := context.Background()
	sink := func(ScanEvent) error { return nil }
	if p, e := Scan(ctx, r, size, s, ScanOptions{}, sink); p.Complete || !errors.Is(e, ErrUnsupported) {
		t.Fatal("strict virtual", e)
	}
	full, e := ScanMaterialized(ctx, r, size, s, ScanOptions{}, sink)
	if e != nil {
		t.Fatal(e)
	}
	opts := []ScanOptions{{MaxPageReads: 1}, {MaxEntries: 1}, {MaxRows: 1}, {MaxRowBytes: 1}}
	for _, o := range opts {
		p, e := ScanMaterialized(ctx, r, size, s, o, sink)
		if p.Complete || !errors.Is(e, ErrLimit) {
			t.Fatalf("limit %+v %v", o, e)
		}
	}
	for _, sentinel := range []error{ErrStopped, io.ErrClosedPipe} {
		p, e := ScanMaterialized(ctx, r, size, s, ScanOptions{}, func(e ScanEvent) error {
			if e.Record != nil {
				return fmt.Errorf("consumer: %w", sentinel)
			}
			return nil
		})
		if p.Complete || p.Records != 1 || p.PageReads >= full.PageReads || !errors.Is(e, sentinel) {
			t.Fatal("early stop", p, e)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if p, e := ScanMaterialized(canceled, r, size, s, ScanOptions{}, sink); p.Complete || p.PageReads != 0 || !errors.Is(e, context.Canceled) {
		t.Fatal("pre-cancel", p, e)
	}
	canceled, cancel = context.WithCancel(ctx)
	p, e := ScanMaterialized(canceled, r, size, s, ScanOptions{}, func(e ScanEvent) error {
		if e.Record != nil {
			cancel()
		}
		return nil
	})
	if p.Complete || p.Records != 1 || !errors.Is(e, context.Canceled) {
		t.Fatal("mid-cancel", p, e)
	}
	for _, o := range []ScanOptions{{CachePages: -1}, {CachePages: 1}, {CachePages: 3}} {
		p, e := ScanMaterializedAuto(ctx, r, size, o, sink)
		if e != nil || !p.Complete || p.Records != full.Records {
			t.Fatal("cache", o, p, e)
		}
	}
	// Exactly enough budget succeeds, the next access is not silently truncated.
	p, e = ScanMaterialized(ctx, r, size, s, ScanOptions{MaxPageReads: full.PageReads, MaxEntries: full.TraversalEntries, MaxRows: full.Records}, sink)
	if e != nil || !p.Complete {
		t.Fatal("exact budget", p, e)
	}
}

type scanFailReader struct {
	io.ReaderAt
	calls, fail int
	err         error
	cancel      context.CancelFunc
}

func (r *scanFailReader) ReadAt(p []byte, off int64) (int, error) {
	r.calls++
	if r.calls == r.fail {
		if r.cancel != nil {
			r.cancel()
			return r.ReaderAt.ReadAt(p, off)
		}
		return 0, r.err
	}
	return r.ReaderAt.ReadAt(p, off)
}
func TestScanFailureAndOwnership(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "tree_compact_initial")
	s.VirtualColumns = nil
	want, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	// Fail after some leaves were delivered, and preserve the exact I/O cause.
	reader := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 10, err: io.ErrUnexpectedEOF}
	report, err := Scan(context.Background(), reader, int64(len(b)), s, ScanOptions{}, func(ScanEvent) error { return nil })
	if report.Complete || report.Records == 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(report, err)
	}
	// A late sibling-chain failure must not mark an already emitted prefix complete.
	last := want.Pages[len(want.Pages)-1].Number
	broken := append([]byte(nil), b...)
	be.PutUint32(broken[int(last)*PageSize+12:], last)
	resealTestPages(broken)
	report, err = Scan(context.Background(), bytes.NewReader(broken), int64(len(broken)), s, ScanOptions{}, func(ScanEvent) error { return nil })
	if report.Complete || report.Records != uint64(len(want.Records)) || !errors.Is(err, ErrCorrupt) {
		t.Fatal("tail validation", report, err)
	}
	if atomic, err := Read(bytes.NewReader(broken), int64(len(broken)), s); atomic != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("atomic partial", err)
	}
	// Mutating delivered metadata/values cannot mutate pending traversal state.
	report, err = Scan(context.Background(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{}, func(e ScanEvent) error {
		if e.Page != nil {
			e.Page.Next = 0
			e.Page.Slots[0] = 0
		}
		if e.Node != nil {
			e.Node.ChildPage = 0
		}
		if e.Record != nil {
			e.Record.Values[0] = nil
		}
		return nil
	})
	if err != nil || !report.Complete {
		t.Fatal("ownership", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader = &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 1, cancel: cancel}
	report, err = Scan(ctx, reader, int64(len(b)), s, ScanOptions{}, func(ScanEvent) error { return nil })
	if report.Complete || report.Pages != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("I/O cancellation", report, err)
	}
}

func TestScanCache(t *testing.T) {
	source := bytes.NewReader(make([]byte, 3*PageSize))
	r, err := newScanReader(context.Background(), source, ScanOptions{CachePages: 1})
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, PageSize)
	for _, off := range []int64{0, 0, PageSize, 0} {
		if _, err = r.ReadAt(b, off); err != nil {
			t.Fatal(err)
		}
		b[0] = 255
	}
	if r.requests != 4 || r.hits != 1 || r.reads != 3 || r.lru.Len() != 1 || b[1] != 0 {
		t.Fatal("LRU counters", r)
	}
	if _, err = r.ReadAt(b, 0); err != nil || b[0] != 0 {
		t.Fatal("cache ownership", err)
	}
}

func loadScanSchema(t testing.TB, path string) Schema {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScanInvalidInputs(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r := bytes.NewReader(b)
	size := int64(len(b))
	ctx := context.Background()
	sink := func(ScanEvent) error { return nil }
	for _, run := range []func() (ScanReport, error){
		func() (ScanReport, error) { return Scan(nil, r, size, s, ScanOptions{}, sink) },
		func() (ScanReport, error) { return Scan(ctx, nil, size, s, ScanOptions{}, sink) },
		func() (ScanReport, error) { return Scan(ctx, r, size, s, ScanOptions{CachePages: -2}, sink) },
		func() (ScanReport, error) { return Scan(ctx, r, size, s, ScanOptions{}, nil) },
	} {
		p, e := run()
		if e == nil || p.Complete {
			t.Fatal(p, e)
		}
	}
	// Direct strict and automatic strict paths both preserve the original row view.
	want, e := Read(r, size, s)
	if e != nil {
		t.Fatal(e)
	}
	for _, auto := range []bool{false, true} {
		got := &Result{Records: make([]Record, 0)}
		var p ScanReport
		if auto {
			p, e = ScanAuto(ctx, r, size, ScanOptions{}, collectScan(got))
		} else {
			p, e = Scan(ctx, r, size, s, ScanOptions{}, collectScan(got))
		}
		if e != nil || !p.Complete || !reflect.DeepEqual(got, want) {
			t.Fatal("strict result", e)
		}
	}
}

func TestScanRowBudgetBeforeLOB(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "lesson_compact_initial")
	size := int64(len(b))
	r := bytes.NewReader(b)
	full, err := Read(r, size, s)
	if err != nil {
		t.Fatal(err)
	}
	row := full.Records[0]
	needed := uint64(row.End - row.Start)
	for _, field := range row.External {
		needed += uint64(field.Length)
	}
	reader := &rejectLOBReader{ReaderAt: r, page: row.External[0].FirstPage}
	report, err := Scan(context.Background(), reader, size, s, ScanOptions{MaxRowBytes: needed - 1}, func(ScanEvent) error { return nil })
	if report.Records != 0 || !errors.Is(err, ErrLimit) || reader.attempted {
		t.Fatal("row budget checked after LOB allocation", report, err)
	}
	report, err = Scan(context.Background(), r, size, s, ScanOptions{MaxRowBytes: needed}, func(ScanEvent) error { return nil })
	if err != nil || !report.Complete {
		t.Fatal("exact row bytes", report, err)
	}
}

type rejectLOBReader struct {
	io.ReaderAt
	page      uint32
	attempted bool
}

func (r *rejectLOBReader) ReadAt(b []byte, off int64) (int, error) {
	if off == int64(r.page)*PageSize {
		r.attempted = true
		return 0, io.ErrClosedPipe
	}
	return r.ReaderAt.ReadAt(b, off)
}
