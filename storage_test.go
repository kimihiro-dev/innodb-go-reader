package innodb

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/bits"
	"strings"
	"testing"
	"unicode/utf8"
)

func checkStorage(t testing.TB, p *StorageReport) {
	t.Helper()
	if p == nil || !p.Complete || p.LocalRecord.Count != p.Rows {
		t.Fatal("incomplete storage report", p)
	}
	local := p.HeaderBytes + p.NullBitmapBytes + p.InstantHeaderBytes + p.SystemBytes + p.DroppedPayloadBytes + p.DroppedLengthBytes
	pages := p.SharedLOBPages
	length := func(s StorageLength) {
		t.Helper()
		var count uint64
		for _, n := range s.Buckets {
			count += n
		}
		if count != s.Count || s.Min > s.Max || s.Count > 0 && (s.Sum < s.Min || s.Sum < s.Max) {
			t.Fatal("length invariants", s)
		}
	}
	length(p.LocalRecord)
	for _, c := range p.Columns {
		if c.Rows != p.Rows || c.Stored+c.Defaults != p.Rows || c.DefaultNulls > c.Nulls || c.Encoded.Count+c.Nulls != p.Rows || c.Local.Count != c.Stored-(c.Nulls-c.DefaultNulls) || c.ExclusiveLOBPageBytes != c.ExclusiveLOBPages*PageSize {
			t.Fatal("column counts", c)
		}
		length(c.Encoded)
		length(c.Local)
		if c.Logical != nil {
			length(*c.Logical)
			if c.Logical.Count != c.Encoded.Count {
				t.Fatal("logical NULL exclusion", c)
			}
		}
		local += c.Local.Sum + c.LengthBytes
		pages += c.ExclusiveLOBPages
	}
	if local != p.LocalRecord.Sum || pages != p.LOBReferencedPages || p.LOBReferencedPageBytes != pages*PageSize || p.UniqueExternalBytes > p.ReferencedExternalBytes {
		t.Fatal("physical decomposition", local, p)
	}
}

func TestStorageLesson(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r := &localPageReader{data: b}
	p, err := AnalyzeStorage(t.Context(), r, int64(len(b)), s, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkStorage(t, p)
	// Independent raw-byte calculation: [148,182), [216,239), [120,148),
	// [182,216). Four INT ids, three scores, two six-byte names and one empty.
	if p.Rows != 4 || p.TreePages != 1 || p.TreePageBytes != PageSize || p.LeafPages != 1 || p.LocalRecord.Sum != 119 || p.LocalRecord.Min != 23 || p.LocalRecord.Max != 34 || p.HeaderBytes != 20 || p.NullBitmapBytes != 4 || p.SystemBytes != 52 || p.InstantHeaderBytes != 0 || p.DroppedPayloadBytes != 0 || p.LOBReferencedPages != 0 {
		t.Fatal("manual lesson accounting", p)
	}
	if len(r.pages) != 2 || r.pages[0] != 0 || r.pages[1] != 4 || p.PageReads != 2 {
		t.Fatal("provenance must not reread every page", r.pages, p.PageReads)
	}
	id, score, name := p.Columns[0], p.Columns[1], p.Columns[2]
	if id.Encoded.Sum != 16 || id.Local.Sum != 16 || id.MaxEncodedBytes != 4 || score.Encoded.Sum != 12 || score.Nulls != 1 || score.Logical != nil || name.Encoded.Sum != 12 || name.Local.Sum != 12 || name.LengthBytes != 3 || name.Nulls != 1 || name.EmptyValues != 1 || name.Logical.Sum != 12 || name.Logical.Count != 3 || name.MaxEncodedBytes != 128 || name.Encoded.Buckets[0] != 1 || name.Encoded.Buckets[3] != 2 {
		t.Fatal("independent SQL and byte lengths", p.Columns)
	}
	j, err := json.Marshal(p)
	if err != nil || !bytes.Contains(j, []byte(`"Sum":"119"`)) || !bytes.Contains(j, []byte(`"Rows":"4"`)) || !bytes.Contains(j, []byte(`"Buckets":["1"`)) {
		t.Fatal("exact JSON", string(j), err)
	}
}

func TestStorageSQL(t *testing.T) {
	for _, tc := range []struct{ dir, name string }{
		{"testdata/compact", "lesson_compact_null_deleted"},
		{"testdata/compact", "lesson_dynamic_grown"},
		{"testdata/compact", "lesson_compact_initial"},
		{"testdata/compact-legacy", "lesson_compact_mixed_formats"},
		{"testdata/compact", "composite_compact_initial"},
		{"testdata/cluster", "rowid_lesson"},
		{"testdata/cluster", "unique_text_tree"},
		{"testdata/generated", "mixed_instant"},
		{"testdata/instant", "lesson_readd"},
		{"testdata/instant", "hidden_drop"},
		{"testdata/instant", "types_defaults"},
		{"testdata/chars", "char_mixed"},
		{"testdata/chars", "char_external"},
		{"testdata/charset", "bytes_latin1"},
		{"testdata/charset", "widths_ascii"},
		{"testdata/charset", "external_utf8mb3"},
		{"testdata/mysql5744", "scalar_dynamic_initial"},
		{"testdata/mysql5744", "lob_compact_shrunk_deleted"},
		{"testdata/mysql5744", "empty_dynamic_initial"},
		{"testdata/mysql80-patches/8.0.28", "instant_compact_mixed"},
	} {
		t.Run(tc.dir+"/"+tc.name, func(t *testing.T) {
			base := tc.dir + "/" + tc.name
			b := unzip(t, base+".ibd.gz")
			var s Schema
			if strings.HasPrefix(tc.dir, "testdata/mysql80-patches/") {
				m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
				if err != nil || m.MaterializedSchema == nil {
					t.Fatal(err)
				}
				s = *m.MaterializedSchema
			} else {
				s = loadScanSchema(t, base+".json")
			}
			var expected [][]any
			raw := unzip(t, base+".expected.json.gz")
			if tc.dir == "testdata/charset" {
				var wire []struct {
					ID    json.Number
					Note  string
					Cells []struct{ Text, Hex *string }
				}
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				for _, row := range wire {
					values := make([]any, len(s.Columns))
					values[0], values[len(values)-1] = row.ID, row.Note
					for i, cell := range row.Cells {
						v := cell.Text
						if s.Columns[i+1].isBinary() {
							v = cell.Hex
						}
						if v != nil {
							values[i+1] = *v
						}
					}
					expected = append(expected, values)
				}
			} else {
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if err := d.Decode(&expected); err != nil {
					t.Fatal(err)
				}
			}
			p, err := AnalyzeStorageMaterialized(t.Context(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{})
			if err != nil {
				t.Fatal(err)
			}
			checkStorage(t, p)
			if p.Rows != uint64(len(expected)) || len(p.VirtualColumns) != len(s.VirtualColumns) {
				t.Fatal("independent SQL rows / VIRTUAL", p.Rows, len(expected))
			}
			for i, c := range s.Columns {
				var nulls, empty uint64
				var logical StorageLength
				for _, row := range expected {
					if row[i] == nil {
						nulls++
						continue
					}
					if c.isText() || c.isBinary() {
						v := row[i].(string)
						n := len(v)
						if c.isBinary() {
							decoded, err := hex.DecodeString(v)
							if err != nil {
								t.Fatal(err)
							}
							n = len(decoded)
						} else if c.charsetWidth() == 1 {
							n = utf8.RuneCountInString(v)
						}
						if n == 0 {
							empty++
						}
						if err := logical.observe(uint64(n)); err != nil {
							t.Fatal(err)
						}
					}
				}
				got := p.Columns[i]
				// This SQL JSON view collapses JSON null and SQL NULL. Do not
				// claim it independently proves JSON SQL NULL counts.
				if c.Type != "JSON" && got.Nulls != nulls || got.EmptyValues != empty || got.Logical != nil && *got.Logical != logical {
					t.Fatal("independent SQL lengths", c.Name, got, logical)
				}
			}
			if tc.name == "lesson_readd" && (p.Columns[0].Defaults == 0 || p.Columns[2].DefaultNulls == 0 || p.DroppedPayloadBytes == 0 || p.InstantHeaderBytes == 0) {
				t.Fatal("INSTANT default/drop accounting", p)
			}
			if tc.name == "lesson_compact_initial" && p.Columns[1].PrefixBytes == 0 {
				t.Fatal("compact prefix", p)
			}
			if tc.name == "rowid_lesson" && p.SystemBytes != p.Rows*19 {
				t.Fatal("hidden ROW_ID is shared overhead")
			}
			// The preserved writer SQL separately inserts SQL NULL in id=2
			// and CAST('null' AS JSON) in id=3; its JSON view alone is ambiguous.
			if tc.name == "scalar_dynamic_initial" && (p.Columns[26].Nulls != 1 || p.Columns[26].Encoded.Count != 2 || p.Columns[26].Encoded.Min != 2 || p.Columns[26].Logical != nil) {
				t.Fatal("JSON null is a stored encoding, not SQL NULL/empty")
			}
		})
	}
}

func TestStorageSharedLOB(t *testing.T) {
	// Synthetic provenance: repeated and overlapping references must not charge
	// capacity twice. Metadata-only pages carry no payload; sharing crosses columns.
	p := &StorageReport{Columns: make([]ColumnStorage, 2)}
	a := storageAnalysis{out: p, lob: map[uint32]*storageLOBPage{}}
	a.lobPage(5, 0, &ByteSpan{100, 200})
	a.lobPage(5, 0, &ByteSpan{100, 200})
	a.lobPage(5, 1, &ByteSpan{150, 250})
	a.lobPage(6, 0, nil)
	a.lobPage(7, 1, &ByteSpan{49, 59})
	a.lobPage(6, 0, nil)
	if err := a.finish(t.Context()); err != nil || p.UniqueExternalBytes != 160 || p.LOBReferencedPages != 3 || p.SharedLOBPages != 1 || p.SharedLOBPageBytes != PageSize || p.Columns[0].ExclusiveLOBPages != 1 || p.Columns[1].ExclusiveLOBPages != 1 || p.LOBReferencedPageBytes != 3*PageSize {
		t.Fatal("synthetic shared provenance", p, err)
	}
}

func TestStorageSharedReferences(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "lesson_dynamic_initial")
	local, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	row := local.Records[0]
	var text, binary ExternalField
	for _, f := range row.External {
		if f.Column == 1 {
			text = f
		} else if f.Column == 2 {
			binary = f
		}
	}
	if text.FirstPage == 0 || binary.FirstPage == 0 {
		t.Fatal("need two current references")
	}
	// Synthetic alias in a disposable copy, keeping lengths/layout unchanged:
	// binary now refers to text's exact current raw value. No ownership/MVCC
	// claim follows from this supported chain reading and accounting test.
	copy(b[int(s.RootPage)*PageSize+binary.Offset:], text.Reference[:])
	resealTestPages(b)
	p, err := AnalyzeStorage(t.Context(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkStorage(t, p)
	if p.SharedLOBPages == 0 || p.UniqueExternalBytes >= p.ReferencedExternalBytes || p.Columns[2].Encoded.Max != p.Columns[1].Encoded.Max || p.Columns[1].ExclusiveLOBPages != 0 || p.Columns[2].ExclusiveLOBPages != 0 {
		t.Fatal("actual shared reference must remain nonadditive", p)
	}
}

func TestStorageDeletedAndNavigation(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	b[4*PageSize+155-5] |= 0x20 // Synthetic delete-mark, still on the record chain.
	resealTestPages(b)
	p, err := AnalyzeStorage(t.Context(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkStorage(t, p)
	if p.Rows != 3 || p.DeletedRecords != 1 || p.DeletedLocalBytes != 34 || p.LocalRecord.Sum != 85 || p.Columns[2].Encoded.Sum != 6 {
		t.Fatal("deleted bytes cannot enter current columns", p)
	}
	b, s = treeFixture(t, "deep_rows")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	node := full.Nodes[0]
	be.PutUint32(b[int(node.PageNumber)*PageSize+node.End-4:], 0)
	resealTestPages(b) // Synthetic navigation damage inside valid CRCs.
	if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("navigation must stay strict", p, err)
	}
}

func TestStorageLengths(t *testing.T) {
	var h StorageLength
	for _, n := range []uint64{0, 1, 2, 3, 4, 7, 8, 255, 256, 65535, 65536} {
		if err := h.observe(n); err != nil || h.Buckets[bits.Len64(n)] == 0 {
			t.Fatal(h, err)
		}
	}
	if h.Count != 11 || h.Min != 0 || h.Max != 65536 || h.Buckets[2] != 2 || h.Buckets[3] != 2 {
		t.Fatal("fixed bucket boundaries", h)
	}
	n := uint64(math.MaxUint64)
	if !errors.Is(storageAdd(&n, 1), ErrLimit) || n != math.MaxUint64 {
		t.Fatal("overflow must not wrap")
	}
	h = StorageLength{Count: math.MaxUint64}
	if !errors.Is(h.observe(0), ErrLimit) {
		t.Fatal("observation overflow")
	}
	h = StorageLength{}
	if err := h.observe(math.MaxUint64); err != nil || h.Buckets[64] != 1 || !errors.Is(h.observe(1), ErrLimit) {
		t.Fatal("uint64 maximum", h, err)
	}
	j, err := json.Marshal(h)
	if err != nil || !bytes.Contains(j, []byte(`"Sum":"18446744073709551615"`)) {
		t.Fatal("JSON must preserve uint64", string(j), err)
	}
}

func TestStorageControl(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	for _, o := range []ScanOptions{{MaxPageReads: 1}, {MaxEntries: 1}, {MaxRows: 1}, {MaxRowBytes: 1}} {
		if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(b), int64(len(b)), s, o); p != nil || !errors.Is(err, ErrLimit) {
			t.Fatal("budget must not return partial success", p, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if p, err := AnalyzeStorage(ctx, bytes.NewReader(b), int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(p, err)
	}
	if p, err := AnalyzeStorage(nil, bytes.NewReader(b), int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal(p, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	r := &diagnosticIOReader{data: b, cancel: cancel}
	if p, err := AnalyzeStorage(ctx, r, int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("mid-scan cancellation", p, err)
	}
	cause := errors.New("storage I/O cause")
	if p, err := AnalyzeStorage(t.Context(), localReadResult{err: cause}, int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, cause) {
		t.Fatal("lost cause", p, err)
	}
	if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(b[:PageSize]), int64(len(b)), s, ScanOptions{}); p != nil || !errors.Is(err, io.EOF) {
		t.Fatal("short reader", p, err)
	}
	bad := bytes.Clone(b)
	bad[4*PageSize+176] ^= 1 // CRC damage: no reseal.
	if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(bad), int64(len(bad)), s, ScanOptions{}); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("CRC must stay strict", p, err)
	}
	gb, gs := generatedFixture(t, "mixed_instant")
	if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(gb), int64(len(gb)), gs, ScanOptions{}); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("VIRTUAL must be explicit", p, err)
	}
	hb, hs, _ := secondaryQueryFixture(t, "secondary_query", "huge", "s")
	if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(hb), int64(len(hb)), hs, ScanOptions{}); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("typed 16 MiB limit", p, err)
	}
	lb, ls := compactFixture(t, "testdata/compact", "lesson_compact_initial")
	baseline, err := AnalyzeStorage(t.Context(), bytes.NewReader(lb), int64(len(lb)), ls, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []ScanOptions{{MaxPageReads: baseline.PageReads - 1}, {MaxEntries: baseline.TraversalEntries - 1}, {MaxRowBytes: 1000}} {
		if p, err := AnalyzeStorage(t.Context(), bytes.NewReader(lb), int64(len(lb)), ls, o); p != nil || !errors.Is(err, ErrLimit) {
			t.Fatal("LOB and tree must share budgets", p, err)
		}
	}
	r = &diagnosticIOReader{data: lb}
	if p, err := AnalyzeStorage(t.Context(), r, int64(len(lb)), ls, ScanOptions{MaxRowBytes: 1000}); p != nil || !errors.Is(err, ErrLimit) || len(r.seen) != 2 {
		t.Fatal("row budget before LOB I/O", r.seen, err)
	}
}

func FuzzStorage(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(uint32(4*PageSize+176), byte(255), true)
	f.Add(uint32(0), byte(1), false)
	f.Fuzz(func(t *testing.T, offset uint32, value byte, reseal bool) {
		mutated := bytes.Clone(b)
		mutated[int(offset)%len(mutated)] ^= value
		if reseal {
			resealTestPages(mutated)
		}
		p, err := AnalyzeStorage(t.Context(), bytes.NewReader(mutated), int64(len(mutated)), s, ScanOptions{MaxPageReads: 100, MaxEntries: 1000, MaxRows: 100})
		if err != nil {
			if p != nil {
				t.Fatal("partial storage report", p)
			}
			return
		}
		checkStorage(t, p)
	})
}
