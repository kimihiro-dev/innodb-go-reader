package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"
)

func TestProjectedLesson(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	var rows [][]any
	p, err := ScanProjected(t.Context(), bytes.NewReader(b), int64(len(b)), s, []string{"name", "score"}, ScanOptions{}, func(e ProjectedEvent) error {
		if e.Record != nil {
			rows = append(rows, e.Record.Values)
			if len(e.Record.Values) != 2 || e.Record.TextBytes[0] == nil && e.Record.Values[0] != nil {
				t.Fatal("projected width/text mapping", e.Record)
			}
		}
		return nil
	})
	// Independent SQL values from the lesson fixture, reordered explicitly.
	want := [][]any{{"InnoDB", int32(42)}, {nil, nil}, {"", int32(0)}, {"你好", int32(-5)}}
	if err != nil || !p.Complete || p.Records != 4 || !reflect.DeepEqual(rows, want) || p.Columns[0].Name != "name" || p.Columns[1].Name != "score" {
		t.Fatal(rows, p, err)
	}
}

func projectedRecords(t *testing.T, b []byte, s Schema, q ClusteredQuery, options ScanOptions) ([]ProjectedRecord, QueryReport, error) {
	t.Helper()
	rows := []ProjectedRecord{}
	p, err := QueryProjectedMaterialized(t.Context(), bytes.NewReader(b), int64(len(b)), s, q, options, func(e ProjectedEvent) error {
		if e.Record != nil {
			rows = append(rows, *e.Record)
		}
		return nil
	})
	return rows, p, err
}

func TestProjectedSQL(t *testing.T) {
	for _, c := range []struct{ dir, name string }{
		{"testdata/compact", "lesson_compact_null_deleted"},
		{"testdata/compact", "composite_compact_initial"},
		{"testdata/cluster", "rowid_lesson"},
		{"testdata/cluster", "unique_text_tree"},
		{"testdata/generated", "mixed_instant"},
		{"testdata/instant", "lesson_readd"},
		{"testdata/instant", "hidden_drop"},
		{"testdata/instant", "types_defaults"},
		{"testdata/chars", "char_mixed"},
		{"testdata/chars", "char_external"},
		{"testdata/mysql5744", "scalar_dynamic_initial"},
		{"testdata/mysql5744", "lob_compact_initial"},
		{"testdata/mysql5744", "empty_dynamic_initial"},
		{"testdata/mysql80-patches/8.0.28", "instant_compact_mixed"},
	} {
		t.Run(c.dir+"/"+c.name, func(t *testing.T) {
			var b []byte
			var s Schema
			base := c.dir + "/" + c.name
			if c.dir == "testdata/mysql80-patches/8.0.28" {
				b = unzip(t, base+".ibd.gz")
				m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
				if err != nil || m.MaterializedSchema == nil {
					t.Fatal(err)
				}
				s = *m.MaterializedSchema
			} else {
				b, s = compactFixture(t, c.dir, c.name)
			}
			var expected [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, base+".expected.json.gz")))
			d.UseNumber()
			if err := d.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			// All columns in reverse declaration order exercises every auxiliary
			// index mapping; a strict subset also drops columns from each row.
			for _, subset := range []bool{false, true} {
				var columns []string
				var indexes []int
				view := Schema{}
				for i := len(s.Columns) - 1; i >= 0; i-- {
					if subset && i%2 != 0 {
						continue
					}
					columns = append(columns, s.Columns[i].Name)
					indexes = append(indexes, i)
					view.Columns = append(view.Columns, s.Columns[i])
				}
				canonical := func(values []any) string {
					if c.dir == "testdata/compact" {
						return compactCanonical(t, values, view)
					}
					return mysql57Row(t, values)
				}
				wanted, actual := map[string]int{}, map[string]int{}
				for _, row := range expected {
					values := make([]any, len(indexes))
					for i, col := range indexes {
						values[i] = row[col]
					}
					wanted[canonical(values)]++
				}
				var scanned []ProjectedRecord
				p, err := ScanProjectedMaterialized(t.Context(), bytes.NewReader(b), int64(len(b)), s, columns, ScanOptions{}, func(e ProjectedEvent) error {
					if e.Record != nil {
						scanned = append(scanned, *e.Record)
						actual[canonical(e.Record.Values)]++
					}
					return nil
				})
				if err != nil || !p.Complete || !reflect.DeepEqual(actual, wanted) || !reflect.DeepEqual(p.Columns, view.Columns) || !reflect.DeepEqual(p.VirtualColumns, s.VirtualColumns) {
					t.Fatal("independent SQL projection", p, err)
				}
				queried, qp, err := projectedRecords(t, b, s, ClusteredQuery{Columns: columns}, ScanOptions{})
				if err != nil || !qp.Complete || !slices.EqualFunc(queried, scanned, func(a, b ProjectedRecord) bool { return reflect.DeepEqual(a, b) }) {
					t.Fatal("scan/query regression", qp, err)
				}
				// Whole-row comparison is regression only; SQL above is independent.
				full, err := ReadMaterialized(bytes.NewReader(b), int64(len(b)), s)
				if err != nil || p.DeletedRecords != uint64(len(full.Result.DeletedRecords)) || p.Nodes != uint64(len(full.Result.Nodes)) {
					t.Fatal("structure regression", err)
				}
				for i, r := range scanned {
					original := full.Result.Records[i]
					if r.PageNumber != original.PageNumber || r.Offset != original.Offset || r.Start != original.Start || r.End != original.End || !reflect.DeepEqual(r.RowVersion, original.RowVersion) || !reflect.DeepEqual(r.RowID, original.RowID) {
						t.Fatal("record provenance mapping")
					}
					for j, col := range indexes {
						if !reflect.DeepEqual(r.Values[j], original.Values[col]) || !bytes.Equal(r.TextBytes[j], original.TextBytes[col]) || r.CharStorage[j] != original.CharStorage[col] || r.SetMasks[j] != original.SetMasks[col] || r.EnumIndexes[j] != original.EnumIndexes[col] || slices.Contains(r.DefaultColumns, j) != slices.Contains(original.DefaultColumns, col) {
							t.Fatal("projected value/auxiliary metadata mapping", j, col)
						}
					}
					for _, f := range r.External {
						matched := false
						for _, old := range original.External {
							if old.Column == indexes[f.Column] {
								old.Column = f.Column
								matched = reflect.DeepEqual(f, old)
							}
						}
						if !matched || len(f.Chunks) == 0 {
							t.Fatal("external provenance mapping")
						}
					}
				}
			}
		})
	}
}

func TestProjectedLOBSelection(t *testing.T) {
	b, s, _ := secondaryQueryFixture(t, "secondary_query", "huge", "s")
	q := ClusteredQuery{Range: PointKey(Key{1}), Columns: []string{"marker"}}
	r := &localPageReader{data: b}
	var values [][]any
	p, err := QueryProjected(t.Context(), r, int64(len(b)), s, q, ScanOptions{}, func(e ProjectedEvent) error {
		if e.Record != nil {
			values = append(values, e.Record.Values)
			if len(e.Record.External) != 0 {
				t.Fatal("unselected LOB exposed as NULL/reference")
			}
		}
		return nil
	})
	if err != nil || !p.Complete || !reflect.DeepEqual(values, [][]any{{int32(17)}}) || !reflect.DeepEqual(r.pages, []uint32{0, 4}) {
		t.Fatal("independent huge-fixture marker and I/O", values, r.pages, p, err)
	}
	local, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), 4, s)
	if err != nil {
		t.Fatal(err)
	}
	localSize := uint64(local.Records[0].End - local.Records[0].Start)
	if _, p, err := projectedRecords(t, b, s, q, ScanOptions{MaxRowBytes: localSize}); err != nil || !p.Complete {
		t.Fatal("unselected suffix charged to row budget", p, err)
	}
	if _, p, err := projectedRecords(t, b, s, q, ScanOptions{MaxRowBytes: localSize - 1}); !errors.Is(err, ErrLimit) || p.Records != 0 || p.Complete {
		t.Fatal("local bytes must still be charged", p, err)
	}
	q.Columns = []string{"payload"}
	if _, p, err := projectedRecords(t, b, s, q, ScanOptions{}); !errors.Is(err, ErrUnsupported) || p.Complete || p.Records != 0 {
		t.Fatal("selected typed value limit", p, err)
	}
	q.Range = PointKey(Key{2})
	rows, p, err := projectedRecords(t, b, s, q, ScanOptions{})
	if err != nil || !p.Complete || len(rows) != 1 || !bytes.Equal(rows[0].Values[0].([]byte), []byte("ab")) {
		t.Fatal("independent small payload", rows, p, err)
	}
	q.Range = PointKey(Key{3})
	if rows, p, err := projectedRecords(t, b, s, q, ScanOptions{}); err != nil || !p.Complete || len(rows) != 0 {
		t.Fatal("unmatched huge value", p, err)
	}

	b, s = compactFixture(t, "testdata/compact", "lesson_compact_initial")
	local, err = ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range local.Records {
		if len(row.External) == 0 {
			continue
		}
		f := row.External[0]
		x := append([]byte{}, b...)
		x[int(f.FirstPage)*PageSize+200] ^= 1 // Actual bad CRC, not resealed.
		q = ClusteredQuery{Range: PointKey(recordQueryKey(Record(row), s)), Columns: []string{"id"}}
		if _, p, err := projectedRecords(t, x, s, q, ScanOptions{}); err != nil || !p.Complete {
			t.Fatal("unselected off-page damage", p, err)
		}
		q.Columns = []string{s.Columns[f.Column].Name}
		if _, p, err := projectedRecords(t, x, s, q, ScanOptions{}); !errors.Is(err, ErrCorrupt) || p.Complete {
			t.Fatal("selected off-page damage", p, err)
		}
		x = append([]byte{}, b...)
		x[int(row.PageNumber)*PageSize+f.Offset+1] ^= 1
		resealTestPages(x) // Synthetic, locally invalid reference with valid CRC.
		q.Columns = []string{"id"}
		if _, p, err := projectedRecords(t, x, s, q, ScanOptions{}); !errors.Is(err, ErrCorrupt) || p.Complete {
			t.Fatal("unselected local reference damage", p, err)
		}
		return
	}
	t.Fatal("no external fixture row")
}

func TestProjectedControl(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	size := int64(len(b))
	for _, columns := range [][]string{{}, {"missing"}, {"id", "id"}, {"ID"}} {
		r := &localPageReader{data: b}
		p, err := ScanProjected(t.Context(), r, size, s, columns, ScanOptions{}, func(ProjectedEvent) error { return nil })
		if !errors.Is(err, ErrUnsupported) || p.Complete || len(r.pages) != 0 {
			t.Fatal("columns must be validated before explicit I/O", columns, p, err)
		}
	}
	bad := s
	bad.RootPage = 0
	if p, err := ScanProjected(t.Context(), bytes.NewReader(b), size, bad, nil, ScanOptions{}, func(ProjectedEvent) error { return nil }); !errors.Is(err, ErrUnsupported) || p.PageReads != 0 || p.Complete {
		t.Fatal("invalid schema", p, err)
	}
	if _, p, err := projectedRecords(t, b, s, ClusteredQuery{Range: PointKey(Key{})}, ScanOptions{}); !errors.Is(err, ErrUnsupported) || p.PageReads != 0 || p.Complete {
		t.Fatal("invalid query", p, err)
	}
	x := append([]byte{}, b...)
	x[4*PageSize+176] = 255 // Unselected name has invalid UTF-8.
	resealTestPages(x)      // Synthetic page envelope; payload remains invalid.
	if _, p, err := projectedRecords(t, x, s, ClusteredQuery{Columns: []string{"id"}}, ScanOptions{}); !errors.Is(err, ErrCorrupt) || p.Complete {
		t.Fatal("unselected inline value must still be validated", p, err)
	}
	for _, o := range []ScanOptions{{MaxRows: 1}, {MaxPageReads: 1}, {MaxEntries: 1}, {MaxRowBytes: 1}} {
		p, err := ScanProjected(t.Context(), bytes.NewReader(b), size, s, []string{"id"}, o, func(ProjectedEvent) error { return nil })
		if !errors.Is(err, ErrLimit) || p.Complete {
			t.Fatal(o, p, err)
		}
	}
	for _, cause := range []error{ErrStopped, errors.New("callback failed")} {
		p, err := QueryProjected(t.Context(), bytes.NewReader(b), size, s, ClusteredQuery{Range: KeyRange{Limit: 1}, Columns: []string{"id"}}, ScanOptions{}, func(e ProjectedEvent) error {
			if e.Record != nil {
				return cause
			}
			return nil
		})
		if !errors.Is(err, cause) || p.Complete || p.LimitReached || p.Records != 1 {
			t.Fatal("callback failure at limit", p, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	p, err := ScanProjected(ctx, bytes.NewReader(b), size, s, []string{"id"}, ScanOptions{}, func(e ProjectedEvent) error {
		if e.Record != nil {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || p.Complete || p.Records != 1 {
		t.Fatal("callback cancellation", p, err)
	}
	cause := errors.New("storage failure")
	r := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 2, err: cause}
	p, err = ScanProjected(t.Context(), r, size, s, nil, ScanOptions{}, func(ProjectedEvent) error { return nil })
	if !errors.Is(err, cause) || p.Complete {
		t.Fatal("I/O cause", p, err)
	}
	for _, input := range []struct {
		ctx context.Context
		r   io.ReaderAt
		o   ScanOptions
	}{{nil, bytes.NewReader(b), ScanOptions{}}, {t.Context(), nil, ScanOptions{}}, {t.Context(), bytes.NewReader(b), ScanOptions{CachePages: -2}}} {
		if _, err := ScanProjected(input.ctx, input.r, size, s, nil, input.o, func(ProjectedEvent) error { return nil }); !errors.Is(err, ErrUnsupported) {
			t.Fatal("invalid scan input", err)
		}
	}
	if _, err := ScanProjected(t.Context(), bytes.NewReader(b), size, s, nil, ScanOptions{}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil callback", err)
	}
}

func TestProjectedRangeSQL(t *testing.T) {
	count := 0
	for _, name := range []string{"signed_rows", "mixed_rows", "unsigned_rows"} {
		b, s := compactFixture(t, "testdata/query", name)
		var queries []struct {
			Name  string
			Query KeyRange
			Rows  [][]any
		}
		d := json.NewDecoder(bytes.NewReader(unzip(t, "testdata/query/"+name+".queries.json.gz")))
		d.UseNumber()
		if err := d.Decode(&queries); err != nil {
			t.Fatal(err)
		}
		last := len(s.Columns) - 1
		for _, c := range queries {
			q := ClusteredQuery{Range: c.Query, Columns: []string{s.Columns[last].Name, s.Columns[0].Name}}
			rows, p, err := projectedRecords(t, b, s, q, ScanOptions{})
			if err != nil || !p.Complete || len(rows) != len(c.Rows) || p.LimitReached != (q.Range.Limit != 0 && uint64(len(rows)) == q.Range.Limit) {
				t.Fatal(name, c.Name, p, err)
			}
			for i, row := range c.Rows {
				if mysql57Row(t, rows[i].Values) != mysql57Row(t, []any{row[last], row[0]}) {
					t.Fatal("independent ordered SQL range/point/prefix", name, c.Name, i)
				}
			}
			count++
		}
	}
	if count != 92 {
		t.Fatal("saved query matrix", count)
	}
}

func TestProjectedViewsAndAuto(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	var rows []ProjectedRecord
	yield := func(e ProjectedEvent) error {
		if e.Record != nil {
			rows = append(rows, *e.Record)
		}
		return nil
	}
	p, err := ScanProjectedAuto(t.Context(), bytes.NewReader(b), int64(len(b)), nil, ScanOptions{}, yield)
	full, fullErr := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil || fullErr != nil || !p.Complete || len(rows) != len(full.Records) {
		t.Fatal(p, err, fullErr)
	}
	for i, r := range rows {
		if !reflect.DeepEqual(Record(r), full.Records[i]) {
			t.Fatal("nil projection selects every stored column")
		}
	}
	rows = nil
	qp, err := QueryProjectedAuto(t.Context(), bytes.NewReader(b), int64(len(b)), ClusteredQuery{Range: PointKey(Key{7}), Columns: []string{"name"}}, ScanOptions{}, yield)
	if err != nil || !qp.Complete || len(rows) != 1 || rows[0].Values[0] != "" {
		t.Fatal("auto query", qp, err)
	}
	q := ClusteredQuery{Range: KeyRange{Lower: &KeyBound{Key{7}, false}, Upper: &KeyBound{Key{7}, true}}, Columns: []string{"name"}}
	r := &localPageReader{data: b}
	qp, err = QueryProjected(t.Context(), r, int64(len(b)), s, q, ScanOptions{}, yield)
	if err != nil || !qp.Complete || qp.Records != 0 || len(r.pages) != 0 {
		t.Fatal("empty range I/O", qp, err)
	}
	b, s = generatedFixture(t, "mixed_instant")
	size := int64(len(b))
	if p, err := ScanProjected(t.Context(), bytes.NewReader(b), size, s, []string{"id"}, ScanOptions{}, yield); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("strict virtual scan", p, err)
	}
	if p, err := ScanProjectedAuto(t.Context(), bytes.NewReader(b), size, []string{"id"}, ScanOptions{}, yield); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("strict virtual auto scan", p, err)
	}
	if p, err := QueryProjectedAuto(t.Context(), bytes.NewReader(b), size, ClusteredQuery{Columns: []string{"id"}}, ScanOptions{}, yield); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("strict virtual auto query", p, err)
	}
	rows = nil
	p, err = ScanProjectedMaterializedAuto(t.Context(), bytes.NewReader(b), size, []string{"secret", "added"}, ScanOptions{}, yield)
	if err != nil || !p.Complete || len(p.VirtualColumns) != 2 || p.Columns[0].Name != "secret" || p.Columns[1].Name != "added" {
		t.Fatal("materialized auto scan", p, err)
	}
	baseline := rows
	rows = nil
	qp, err = QueryProjectedMaterializedAuto(t.Context(), bytes.NewReader(b), size, ClusteredQuery{Columns: []string{"secret", "added"}}, ScanOptions{}, yield)
	if err != nil || !qp.Complete || !reflect.DeepEqual(rows, baseline) || !reflect.DeepEqual(qp.VirtualColumns, s.VirtualColumns) {
		t.Fatal("materialized auto query", qp, err)
	}
	if p, err := ScanProjectedMaterialized(t.Context(), bytes.NewReader(b), size, s, []string{"virt"}, ScanOptions{}, yield); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("virtual cannot enter projection", p, err)
	}
}

func TestProjectedBoundariesAndMutation(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	for _, headerOffset := range []int{8, 12} {
		x := append([]byte{}, b...)
		be.PutUint32(x[4*PageSize+headerOffset:], 6)
		resealTestPages(x) // Synthetic sibling boundary, never a fixed fixture.
		p, err := ScanProjected(t.Context(), bytes.NewReader(x), int64(len(x)), s, []string{"id"}, ScanOptions{}, func(ProjectedEvent) error { return nil })
		if !errors.Is(err, ErrCorrupt) || p.Complete {
			t.Fatal("projection scan relaxed tree boundary", headerOffset, p, err)
		}
	}
	b, s = compactFixture(t, "testdata/compact", "composite_compact_initial")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	p, err := ScanProjected(t.Context(), bytes.NewReader(b), int64(len(b)), s, []string{"k", "txt"}, ScanOptions{}, func(e ProjectedEvent) error {
		if e.Page != nil {
			e.Page.Slots[0] = 0
		}
		if e.Node != nil {
			e.Node.ChildPage = 0
		}
		if e.Record != nil {
			if !reflect.DeepEqual(e.Record.Values[0], full.Records[seen].Values[1]) || e.Record.Values[1] != full.Records[seen].Values[2] {
				t.Fatal("prior callback mutated later row")
			}
			key := e.Record.Values[0].([]byte)
			if len(key) != 0 {
				key[0] ^= 255
			}
			e.Record.Values[0] = nil
			if e.Record.TextBytes != nil {
				e.Record.TextBytes[1] = []byte("changed")
			}
			seen++
		}
		return nil
	})
	if err != nil || !p.Complete || seen != len(full.Records) {
		t.Fatal("projection callback ownership", p, err)
	}
}

func TestProjectedInstantBudget(t *testing.T) {
	b, s := instantFixture(t, "lesson_readd")
	local, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	r := Record(local.Records[0])
	localSize := uint64(r.End - r.Start)
	q := ClusteredQuery{Range: PointKey(recordQueryKey(r, s)), Columns: []string{"b"}}
	if _, p, err := projectedRecords(t, b, s, q, ScanOptions{MaxRowBytes: localSize}); err != nil || !p.Complete {
		t.Fatal("unselected default charged", p, err)
	}
	q.Columns = []string{"a"} // The ADD-time encoded default is exactly "again".
	for _, extra := range []uint64{4, 5} {
		rows, p, err := projectedRecords(t, b, s, q, ScanOptions{MaxRowBytes: localSize + extra})
		if extra == 4 {
			if !errors.Is(err, ErrLimit) || p.Complete {
				t.Fatal("default bytes not charged", p, err)
			}
		} else if err != nil || !p.Complete || len(rows) != 1 || rows[0].Values[0] != "again" || !reflect.DeepEqual(rows[0].DefaultColumns, []int{0}) {
			t.Fatal("default budget/source mapping", p, err)
		}
	}
}

func TestProjectedNavigation(t *testing.T) {
	b, s := treeFixture(t, "deep_rows")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	mid := full.Records[len(full.Records)/2]
	q := ClusteredQuery{Range: PointKey(recordQueryKey(mid, s)), Columns: []string{s.Columns[0].Name}}
	rows, p, err := projectedRecords(t, b, s, q, ScanOptions{})
	if err != nil || !p.Complete || len(rows) != 1 || p.Pages != uint64(full.Page.Level)+1 || p.Pages >= uint64(len(full.Pages)) {
		t.Fatal("projection point did not prune navigation", p, err)
	}
	x := append([]byte{}, b...)
	x[int(full.Records[0].PageNumber)*PageSize+200] ^= 1 // Unvisited page, no CRC reseal.
	if _, p, err := projectedRecords(t, x, s, q, ScanOptions{}); err != nil || !p.Complete {
		t.Fatal("unvisited page damage", p, err)
	}
	if p, err := ScanProjected(t.Context(), bytes.NewReader(x), int64(len(x)), s, q.Columns, ScanOptions{}, func(ProjectedEvent) error { return nil }); !errors.Is(err, ErrCorrupt) || p.Complete {
		t.Fatal("full projection scan missed damaged page", p, err)
	}
}

func FuzzProjectedRanges(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(int8(-3), int8(12), uint8(3), uint8(0))
	f.Add(int8(2), int8(7), uint8(7), uint8(1))
	f.Fuzz(func(t *testing.T, low, high int8, flags, limit uint8) {
		lower, upper, reverse := flags&1 != 0, flags&2 != 0, flags&4 != 0
		q := ClusteredQuery{Range: KeyRange{Lower: &KeyBound{Key{low}, lower}, Upper: &KeyBound{Key{high}, upper}, Reverse: reverse, Limit: uint64(limit)}, Columns: []string{"name", "score"}}
		ids := []int8{-3, 2, 7, 12}
		values := [][]any{{"InnoDB", int32(42)}, {nil, nil}, {"", int32(0)}, {"你好", int32(-5)}}
		want := [][]any{}
		for i, id := range ids {
			if (id > low || lower && id == low) && (id < high || upper && id == high) {
				want = append(want, values[i])
			}
		}
		if reverse {
			slices.Reverse(want)
		}
		reached := limit != 0 && len(want) >= int(limit)
		if reached {
			want = want[:int(limit)]
		}
		rows, p, err := projectedRecords(t, b, s, q, ScanOptions{})
		actual := [][]any{}
		for _, r := range rows {
			actual = append(actual, r.Values)
		}
		if err != nil || !p.Complete || p.LimitReached != reached || !reflect.DeepEqual(actual, want) {
			t.Fatal("independent ordered projection", low, high, flags, limit, actual, want, p, err)
		}
	})
}
