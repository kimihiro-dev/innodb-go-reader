package stats

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	innodb "github.com/kimihiro-dev/innodb-go-reader"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var views = []string{"summary", "page-types", "indexes", "levels", "segments", "regions"}

func fixture(t *testing.T, path string) []byte {
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
func build(t *testing.T, b []byte, o Options) *Report {
	t.Helper()
	r, err := Build(context.Background(), bytes.NewReader(b), int64(len(b)), o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func aggregated(t *testing.T, s *innodb.SpaceReport, view string) *Report {
	t.Helper()
	r, err := aggregate(context.Background(), s, Options{View: view})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Literal expectations are independently readable from the fixed lesson page
// bytes and captured SQL, rather than copied from another Go report.
func TestLessonViews(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	id, zero := uint64(249), uint16(0)
	r := build(t, b, Options{View: "levels", IndexID: &id, Level: &zero})
	g := r.Groups.([]Level)[0]
	if g.IndexID != 249 || g.Level != 0 || g.Pages != 1 || g.LinkedRecords != 4 || g.DeleteMarkedRecords != 0 || g.FreeListRecords != 0 || g.HeapBytes != 119 || g.LayoutUsedBytes != 119 || g.GarbageBytes != 0 || g.ContiguousFreeBytes != 16133 || g.DirectoryBytes != 4 || *g.LayoutOccupancy != (Ratio{119, 16384}) || !reflect.DeepEqual(g.LayoutOccupancyMin, g.LayoutOccupancyMax) {
		t.Fatalf("lesson layout: %+v", g)
	}
	// Hand-decoded INDEX fields: +40 heap top, +38 directory count, +44
	// PAGE_FREE (+44), garbage (+46), +54 records, +64 level, +66 index ID, all big endian.
	p := b[4*16384 : 5*16384]
	if binary.BigEndian.Uint16(p[40:42]) != 239 || binary.BigEndian.Uint16(p[38:40]) != 2 || binary.BigEndian.Uint16(p[46:48]) != 0 || binary.BigEndian.Uint16(p[54:56]) != 4 || binary.BigEndian.Uint16(p[64:66]) != 0 || binary.BigEndian.Uint64(p[66:74]) != 249 {
		t.Fatal("fixed page bytes changed")
	}
	if percent(g.LayoutOccupancy) != "0.73%" {
		t.Fatal("percent")
	}
	id = 1
	zero = 1
	if *r.Filter.IndexID != 249 || *r.Filter.Level != 0 {
		t.Fatal("borrowed filters")
	}

	s := build(t, b, Options{}).Groups.([]Summary)[0]
	if s.FilePages != 7 || s.FileBytes != 114688 || s.UsedPages != 5 || s.FreePages != 2 || s.UninitializedPages != 0 || s.TailPages != 0 || s.ZeroPages != 2 || s.IndexCount != 2 || s.SegmentCount != 4 || s.UnknownUsedPages != 0 || *s.UsedShare != (Ratio{5, 7}) {
		t.Fatalf("summary: %+v", s)
	}
	types := build(t, b, Options{View: "page-types"}).Groups.([]PageType)
	if len(types) != 5 {
		t.Fatal(types)
	}
	for i, code := range []uint16{3, 5, 8, 17853, 17855} {
		if types[i].TypeCode != code || types[i].Pages != 1 || types[i].Bytes != 16384 || *types[i].ShareOfUsedPages != (Ratio{1, 5}) {
			t.Fatal(types)
		}
	}
	indexes := build(t, b, Options{View: "indexes"}).Groups.([]Index)
	wantIndexes := []Index{{249, "INDEX", 4, 1, 1, 1, 0, 4, 0, 4, 0}, {math.MaxUint64, "SDI", 3, 1, 1, 1, 0, 2, 0, 2, 0}}
	if !reflect.DeepEqual(indexes, wantIndexes) {
		t.Fatal(indexes)
	}
	segments := build(t, b, Options{View: "segments"}).Groups.([]Segment)
	if len(segments) != 4 {
		t.Fatal(segments)
	}
	for i, g := range segments {
		if g.SegmentID != uint64(i+1) || g.ReservedUnusedPages != 0 {
			t.Fatal(g)
		}
		if i%2 == 0 {
			if g.Role != "top" || g.UsedPages != 1 || g.ReservedPages != 1 || *g.AllocationUtilization != (Ratio{1, 1}) {
				t.Fatal(g)
			}
		} else if g.Role != "leaf" || g.UsedPages != 0 || g.ReservedPages != 0 || g.AllocationUtilization != nil {
			t.Fatal(g)
		}
	}
	regions := build(t, b, Options{View: "regions"}).Groups.([]Region)
	if len(regions) != 6 || regions[5].FirstPage != 5 || regions[5].EndPage != 7 || regions[5].Pages != 2 || regions[5].Allocation != "free" || regions[5].TypeCode != nil || regions[5].IndexID != nil || regions[5].SegmentID != nil {
		t.Fatal(regions)
	}
	if *regions[3].IndexID != math.MaxUint64 || *regions[4].IndexID != 249 || *regions[4].SegmentID != 3 {
		t.Fatal(regions)
	}
}

func TestJSONPrecisionAndDeterminism(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, view := range views {
		t.Run(view, func(t *testing.T) {
			r := build(t, b, Options{View: view})
			var first bytes.Buffer
			if err := WriteJSON(context.Background(), &first, r); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(first.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value["schema_version"] != float64(1) || value["complete"] != true {
				t.Fatal(value)
			}
			delete(value, "schema_version")
			var check func(any)
			check = func(v any) {
				switch x := v.(type) {
				case float64:
					t.Fatalf("numeric data field loses precision: %v", x)
				case map[string]any:
					for _, v := range x {
						check(v)
					}
				case []any:
					for _, v := range x {
						check(v)
					}
				}
			}
			check(value)
			if bytes.Contains(first.Bytes(), []byte(`"Raw"`)) || bytes.Contains(first.Bytes(), []byte(`"Pages"`)) {
				t.Fatal("full source copied")
			}
			for i := 0; i < 4; i++ {
				var again bytes.Buffer
				if err := WriteJSON(context.Background(), &again, build(t, b, Options{View: view})); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(first.Bytes(), again.Bytes()) {
					t.Fatal("unstable grouping order")
				}
			}
		})
	}
	id := uint64(math.MaxUint64)
	r := build(t, b, Options{View: "indexes", IndexID: &id})
	var out bytes.Buffer
	if err := WriteJSON(context.Background(), &out, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"index_id":"18446744073709551615"`)) {
		t.Fatal(out.String())
	}
	if len(r.Groups.([]Index)) != 1 || r.Groups.([]Index)[0].Kind != "SDI" {
		t.Fatal("SDI filter")
	}
}

func TestSpecialFixtures(t *testing.T) {
	for _, path := range []string{"secondary/empty.ibd.gz", "secondary/deep.ibd.gz", "secondary/compact.ibd.gz", "changes/lesson_marked.ibd.gz", "changes/lesson_reused.ibd.gz", "compact/tree_compact_initial.ibd.gz", "space/grown.space.gz", "space/deleted.space.gz"} {
		t.Run(path, func(t *testing.T) {
			b := fixture(t, path)
			s, err := innodb.AnalyzeSpace(context.Background(), bytes.NewReader(b), int64(len(b)), innodb.SpaceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			summary := aggregated(t, s, "summary").Groups.([]Summary)[0]
			if summary.FilePages != summary.UsedPages+summary.FreePages+summary.UninitializedPages+summary.TailPages {
				t.Fatal("allocation sum")
			}
			// Independent raw XDES two-bit bitmap decoding, including uninitialized
			// and file-tail boundaries, without SpaceReport allocation fields.
			counts := rawAllocations(b)
			if counts != [4]uint64{summary.UsedPages, summary.FreePages, summary.UninitializedPages, summary.TailPages} {
				t.Fatal("raw bitmap", counts, summary)
			}
			types := aggregated(t, s, "page-types").Groups.([]PageType)
			total := uint64(0)
			for _, g := range types {
				total += g.Pages
			}
			if total != s.UsedPages {
				t.Fatal("type sum")
			}
			indexes := aggregated(t, s, "indexes").Groups.([]Index)
			levels := aggregated(t, s, "levels").Groups.([]Level)
			// Read the fixed-width physical headers independently of SpaceIndexPage.
			raw := rawLevels(b)
			for _, g := range levels {
				expected := raw[[2]uint64{g.IndexID, uint64(g.Level)}]
				if expected == nil || g.Pages != expected.Pages || g.LinkedRecords != expected.LinkedRecords || g.HeapBytes != expected.HeapBytes || g.LayoutUsedBytes != expected.LayoutUsedBytes || g.GarbageBytes != expected.GarbageBytes || g.ContiguousFreeBytes != expected.ContiguousFreeBytes || g.DirectoryBytes != expected.DirectoryBytes || !reflect.DeepEqual(g.LayoutOccupancyMin, expected.LayoutOccupancyMin) || !reflect.DeepEqual(g.LayoutOccupancyMax, expected.LayoutOccupancyMax) {
					t.Fatal("raw local headers", g, expected)
				}
			}

			marked, garbage, freeRecords := uint64(0), uint64(0), uint64(0)
			for _, idx := range indexes {
				pages, leaves, records, deleted := uint64(0), uint64(0), uint64(0), uint64(0)
				for _, g := range levels {
					if g.IndexID != idx.IndexID {
						continue
					}
					pages += g.Pages
					if g.Level == 0 {
						leaves += g.Pages
						records += g.LinkedRecords
						deleted += g.DeleteMarkedRecords
					}
					if g.LayoutUsedBytes+g.GarbageBytes != g.HeapBytes || *g.LayoutOccupancy != (Ratio{g.LayoutUsedBytes, g.Pages * 16384}) || g.LayoutOccupancyMin.Numerator > g.LayoutOccupancyMax.Numerator {
						t.Fatal("layout identity", g)
					}
				}
				if pages != idx.Pages || leaves != idx.LeafPages || idx.InternalPages+leaves != pages || records != idx.LeafLinkedRecords || deleted != idx.LeafDeleteMarked || records-deleted != idx.LeafNonDeleted {
					t.Fatal("index-level totals", idx)
				}
			}
			for _, g := range levels {
				marked += g.DeleteMarkedRecords
				garbage += g.GarbageBytes
				freeRecords += g.FreeListRecords
			}
			if path == "changes/lesson_marked.ibd.gz" && marked == 0 {
				t.Fatal("missing delete marks")
			}
			if path == "changes/lesson_reused.ibd.gz" && garbage == 0 && freeRecords == 0 {
				t.Fatal("missing PAGE_FREE/garbage")
			}
			if path == "secondary/deep.ibd.gz" {
				deep := 0
				for _, idx := range indexes {
					if idx.Height == 3 {
						deep++
					}
				}
				if deep < 2 {
					t.Fatal("need three-level cluster/secondary", indexes)
				}
			}
			regions := aggregated(t, s, "regions").Groups.([]Region)
			end := uint64(0)
			for _, g := range regions {
				if g.FirstPage != end || g.Pages != g.EndPage-g.FirstPage {
					t.Fatal("region coverage", g)
				}
				end = g.EndPage
				if g.Allocation != "used" && (g.TypeCode != nil || g.IndexID != nil || g.SegmentID != nil) {
					t.Fatal("stale identity", g)
				}
			}
			if end != s.FilePages {
				t.Fatal("incomplete regions")
			}
			segments := aggregated(t, s, "segments").Groups.([]Segment)
			for _, g := range segments {
				if g.ReservedUnusedPages+g.UsedPages != g.ReservedPages {
					t.Fatal("segment sum")
				}
			}
			if strings.HasPrefix(path, "space/") {
				var sql struct {
					Indexes []struct {
						ID          uint64
						Page, Space uint32
					}
					Rows uint64
				}
				name := strings.TrimSuffix(strings.TrimPrefix(path, "space/"), ".space.gz")
				if err := json.Unmarshal(fixture(t, "space/"+name+".sql-metadata.json.gz"), &sql); err != nil {
					t.Fatal(err)
				}
				for _, expected := range sql.Indexes {
					found := false
					for _, idx := range indexes {
						if idx.IndexID == expected.ID {
							found = true
							if idx.RootPage != expected.Page || s.SpaceID != expected.Space || idx.LeafNonDeleted != sql.Rows {
								t.Fatal("captured SQL", expected, idx)
							}
						}
					}
					if !found {
						t.Fatal("missing SQL index", expected)
					}
				}
			}
			if path == "space/grown.space.gz" {
				if summary.FilePages != 17408 || summary.UsedPages != 17080 || summary.FreePages != 136 || summary.UninitializedPages != 192 {
					t.Fatal("grown bitmap counts", summary)
				}
				leased := false
				for _, e := range s.Extents {
					if e.State == 5 {
						leased = true
						// The leased full extent has 62 segment pages, after its two
						// public descriptor/bitmap pages. Avoid a fixed 64-page rule.
						if e.Present != 64 || e.Used != 64 {
							t.Fatal("leased sample", e)
						}
						for _, g := range segments {
							if g.SegmentID == e.SegmentID {
								// Verify the whole segment directly from its allocated page
								// ownership, and reserve each extent's real present pages.
								used, reserved := uint64(0), uint64(0)
								for _, p := range s.Pages {
									if p.Allocation == "used" && p.SegmentID == g.SegmentID {
										used++
									}
								}
								for _, seg := range s.Segments {
									if seg.ID == g.SegmentID {
										reserved += uint64(len(seg.FragmentPages))
									}
								}
								for _, other := range s.Extents {
									if (other.State == 4 || other.State == 5) && other.SegmentID == g.SegmentID {
										reserved += uint64(other.Present)
										if other.State == 5 {
											reserved -= 2
										}
									}
								}
								if g.UsedPages != used || g.ReservedPages != reserved {
									t.Fatal("leased counts", g, used, reserved)
								}
							}
						}
					}
				}
				if !leased || s.Pages[16384].Type != 9 || s.Pages[16385].Type != 5 {
					t.Fatal("second descriptor group")
				}
			}
		})
	}
}

func rawAllocations(b []byte) [4]uint64 {
	var counts [4]uint64
	size, limit := binary.BigEndian.Uint32(b[46:50]), binary.BigEndian.Uint32(b[50:54])
	for n := uint32(0); n < uint32(len(b)/16384); n++ {
		category := 0
		if n >= size {
			category = 3
		} else if n >= limit {
			category = 2
		} else {
			descriptorPage := (n / 16384) * 16384
			descriptorOffset := 150 + ((n%16384)/64)*40
			bit := 2 * (n % 64)
			if b[int(descriptorPage)*16384+int(descriptorOffset)+24+int(bit/8)]&(1<<uint(bit%8)) != 0 {
				category = 1
			}
		}
		counts[category]++
	}
	return counts
}

// Only synthetic payload/type mutations reseal CRC; CRC failures remain unsealed.
func reseal(p []byte) {
	c := crc32.Checksum(p[4:26], crc32.MakeTable(crc32.Castagnoli)) ^ crc32.Checksum(p[38:len(p)-8], crc32.MakeTable(crc32.Castagnoli))
	binary.BigEndian.PutUint32(p[:4], c)
	binary.BigEndian.PutUint32(p[len(p)-8:], c)
	copy(p[len(p)-4:], p[20:24])
}
func TestUnknownAndStalePages(t *testing.T) {
	b := fixture(t, "secondary_query/huge.ibd.gz")
	binary.BigEndian.PutUint16(b[6*16384+24:], 65000)
	reseal(b[6*16384 : 7*16384])
	r := build(t, b, Options{})
	if r.Groups.([]Summary)[0].UnknownUsedPages != 1 {
		t.Fatal("unknown count")
	}
	types := build(t, b, Options{View: "page-types"}).Groups.([]PageType)
	found := false
	for _, g := range types {
		if g.TypeCode == 65000 {
			found = true
			if g.TypeName != "UNKNOWN" || g.Pages != 1 {
				t.Fatal(g)
			}
		}
	}
	if !found {
		t.Fatal("unknown omitted")
	}
	regions := build(t, b, Options{View: "regions"}).Groups.([]Region)
	found = false
	for _, g := range regions {
		if g.FirstPage <= 6 && g.EndPage > 6 {
			found = true
			if g.TypeCode == nil || *g.TypeCode != 65000 || g.IndexID == nil || g.SegmentID == nil {
				t.Fatal("unknown allocated owner", g)
			}
		}
	}
	if !found {
		t.Fatal("unknown region omitted")
	}
	b = fixture(t, "mysql8045/lesson_rows.ibd")
	copy(b[5*16384:6*16384], b[4*16384:5*16384])
	copy(b[6*16384:7*16384], b[3*16384:4*16384])
	r = build(t, b, Options{View: "regions"})
	last := r.Groups.([]Region)[5]
	if last.FirstPage != 5 || last.EndPage != 7 || last.TypeCode != nil || last.IndexID != nil || last.SegmentID != nil {
		t.Fatal("stale free headers split region", last)
	}
}

func TestRegionGroupingAndRatios(t *testing.T) {
	// Synthetic already-validated metadata isolates identity boundaries, absent
	// owners and zero denominators; it is not new physical compatibility evidence.
	code := uint16(17855)
	s := &innodb.SpaceReport{FilePages: 8, Segments: []innodb.SpaceSegment{{ID: 5, IndexID: 11}}, Pages: []innodb.SpacePage{
		{Number: 0, Allocation: "used", Type: code, SegmentID: 5},
		{Number: 1, Allocation: "used", Type: code, SegmentID: 5},
		{Number: 2, Allocation: "used", Type: code, SegmentID: 6},
		{Number: 3, Allocation: "used", Type: 65000, SegmentID: 6},
		{Number: 4, Allocation: "free", Type: code, SegmentID: 5},
		{Number: 5, Allocation: "free", Type: 65000, SegmentID: 6},
		{Number: 6, Allocation: "uninitialized"},
		{Number: 7, Allocation: "file-tail"},
	}}
	g := aggregated(t, s, "regions").Groups.([]Region)
	if len(g) != 6 || g[0].Pages != 2 || *g[0].IndexID != 11 || g[1].IndexID != nil || g[3].Pages != 2 || g[4].Allocation != "uninitialized" || g[5].Allocation != "file-tail" {
		t.Fatal(g)
	}
	if ratio(0, 0) != nil || percent(nil) != "—" || percent(ratio(0, 7)) != "0.00%" || percent(ratio(7, 7)) != "100.00%" {
		t.Fatal("ratio endpoints")
	}
	unassigned := aggregated(t, s, "segments").Groups.([]Segment)[0]
	if unassigned.Role != "unassigned" {
		t.Fatal("absent role")
	}
	// Deliberately shuffled synthetic type counts verify descending count, then code.
	s.UsedPages = 8
	s.UsedPageTypes = map[uint16]uint64{17855: 3, 5: 2, 8: 3}
	types := aggregated(t, s, "page-types").Groups.([]PageType)
	if types[0].TypeCode != 8 || types[1].TypeCode != 17855 || types[2].TypeCode != 5 {
		t.Fatal(types)
	}
}

type failReader struct {
	r      io.ReaderAt
	calls  int
	cancel context.CancelFunc
}

func (r *failReader) ReadAt(b []byte, off int64) (int, error) {
	r.calls++
	if r.calls == 3 {
		if r.cancel != nil {
			r.cancel()
		} else {
			return 0, io.ErrClosedPipe
		}
	}
	return r.r.ReadAt(b, off)
}
func TestFiltersAndFailures(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	id, absent, zero, one := uint64(249), uint64(123), uint16(0), uint16(1)
	invalid := []Options{{View: "unknown"}, {View: "summary", IndexID: &id}, {View: "page-types", IndexID: &id}, {View: "regions", IndexID: &id}, {View: "indexes", Level: &zero}, {View: "levels", Level: &zero}, {View: "levels", IndexID: new(uint64)}}
	for _, o := range invalid {
		reader := &failReader{r: bytes.NewReader(b)}
		r, err := Build(context.Background(), reader, int64(len(b)), o)
		if r != nil || !errors.Is(err, innodb.ErrUnsupported) || reader.calls != 0 {
			t.Fatal(o, r, err, reader.calls)
		}
	}
	for _, o := range []Options{{View: "indexes", IndexID: &absent}, {View: "levels", IndexID: &id, Level: &one}, {View: "segments", IndexID: &absent}} {
		if r, err := Build(context.Background(), bytes.NewReader(b), int64(len(b)), o); r != nil || err == nil {
			t.Fatal("missing filter", o, r, err)
		}
	}
	for _, view := range []string{"indexes", "levels", "segments"} {
		r := build(t, b, Options{View: view, IndexID: &id})
		if r.Filter.IndexID == nil || *r.Filter.IndexID != id {
			t.Fatal("filter")
		}
	}
	for _, space := range []innodb.SpaceOptions{{MaxPages: 6}, {MaxEntries: 1}} {
		r, err := Build(context.Background(), bytes.NewReader(b), int64(len(b)), Options{Space: space})
		if r != nil || !errors.Is(err, innodb.ErrLimit) {
			t.Fatal("budget", r, err)
		}
	}
	x := append([]byte{}, b...)
	x[3*16384+200] ^= 1
	if r, err := Build(context.Background(), bytes.NewReader(x), int64(len(x)), Options{View: "levels", IndexID: &id}); r != nil || !errors.Is(err, innodb.ErrCorrupt) {
		t.Fatal("filter skipped corrupt SDI", r, err)
	}
	if r, err := Build(context.Background(), &failReader{r: bytes.NewReader(b)}, int64(len(b)), Options{}); r != nil || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("I/O", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if r, err := Build(ctx, &failReader{r: bytes.NewReader(b), cancel: cancel}, int64(len(b)), Options{}); r != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancel", r, err)
	}
	for _, size := range []int64{0, 1, int64(len(b)) - 1} {
		if r, err := Build(context.Background(), bytes.NewReader(b), size, Options{}); r != nil || !errors.Is(err, innodb.ErrCorrupt) {
			t.Fatal("size", r, err)
		}
	}
	if r, err := Build(nil, bytes.NewReader(b), int64(len(b)), Options{}); r != nil || !errors.Is(err, innodb.ErrUnsupported) {
		t.Fatal("nil context")
	}
	if r, err := Build(context.Background(), nil, int64(len(b)), Options{}); r != nil || !errors.Is(err, innodb.ErrUnsupported) {
		t.Fatal("nil reader")
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestWritersAndCancellation(t *testing.T) {
	b := fixture(t, "mysql8045/lesson_rows.ibd")
	for _, view := range views {
		r := build(t, b, Options{View: view})
		for _, write := range []func(context.Context, io.Writer, *Report) error{WriteJSON, WriteTable} {
			var out bytes.Buffer
			if err := write(context.Background(), &out, r); err != nil || out.Len() == 0 {
				t.Fatal(view, err)
			}
			if err := write(context.Background(), shortWriter{}, r); !errors.Is(err, io.ErrShortWrite) {
				t.Fatal("short write", view, err)
			}
			if err := write(context.Background(), brokenWriter{}, r); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("broken pipe", view, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			out.Reset()
			if err := write(ctx, &out, r); !errors.Is(err, context.Canceled) || out.Len() != 0 {
				t.Fatal("cancelled output", view, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := aggregate(ctx, &innodb.SpaceReport{}, Options{View: "summary"}); r != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("aggregate cancellation", r, err)
	}
}

// This oracle reads documented fixed offsets and XDES bits, not production
// SpaceReport fields. It does not attempt schema-dependent record decoding.
func rawLevels(b []byte) map[[2]uint64]*Level {
	out := map[[2]uint64]*Level{}
	size, limit := binary.BigEndian.Uint32(b[46:50]), binary.BigEndian.Uint32(b[50:54])
	for n := uint32(0); n < uint32(len(b)/16384) && n < size && n < limit; n++ {
		descriptorPage := (n / 16384) * 16384
		descriptorOffset := 150 + ((n%16384)/64)*40
		bit := 2 * (n % 64)
		if b[int(descriptorPage)*16384+int(descriptorOffset)+24+int(bit/8)]&(1<<uint(bit%8)) != 0 {
			continue
		}
		p := b[int(n)*16384 : int(n+1)*16384]
		code := binary.BigEndian.Uint16(p[24:26])
		if code != 17855 && code != 17853 {
			continue
		}
		id, level := binary.BigEndian.Uint64(p[66:74]), binary.BigEndian.Uint16(p[64:66])
		key := [2]uint64{id, uint64(level)}
		g := out[key]
		if g == nil {
			g = &Level{}
			out[key] = g
		}
		heapTop := uint64(binary.BigEndian.Uint16(p[40:42]))
		directory := 2 * uint64(binary.BigEndian.Uint16(p[38:40]))
		garbage := uint64(binary.BigEndian.Uint16(p[46:48]))
		layout := heapTop - 120 - garbage
		if g.Pages == 0 || layout < g.LayoutOccupancyMin.Numerator {
			g.LayoutOccupancyMin = &Ratio{layout, 16384}
		}
		if g.Pages == 0 || layout > g.LayoutOccupancyMax.Numerator {
			g.LayoutOccupancyMax = &Ratio{layout, 16384}
		}
		g.Pages++
		g.LinkedRecords += uint64(binary.BigEndian.Uint16(p[54:56]))
		g.HeapBytes += heapTop - 120
		g.LayoutUsedBytes += layout
		g.GarbageBytes += garbage
		g.ContiguousFreeBytes += 16384 - 8 - directory - heapTop
		g.DirectoryBytes += directory
	}
	return out
}
