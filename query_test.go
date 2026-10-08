package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func recordQueryKey(r Record, s Schema) Key {
	if r.RowID != nil {
		return Key{*r.RowID}
	}
	pk, _ := s.validate()
	key := make(Key, len(pk))
	for i, col := range pk {
		key[i] = r.Values[col]
	}
	return key
}
func queryRecords(ctx context.Context, r io.ReaderAt, size int64, s Schema, q KeyRange, o ScanOptions) ([]Record, QueryReport, error) {
	rows := []Record{}
	report, err := QueryMaterialized(ctx, r, size, s, q, o, func(e ScanEvent) error {
		if e.Record != nil {
			rows = append(rows, *e.Record)
		}
		return nil
	})
	return rows, report, err
}

func TestQueryFixtureMatrix(t *testing.T) {
	paths, err := filepath.Glob("testdata/*/*.ibd*")
	if err != nil {
		t.Fatal(err)
	}
	assets, points, ranges, rows := 0, 0, 0, 0
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			b := scanFixture(t, path)
			r := bytes.NewReader(b)
			size := int64(len(b))
			want, err := ReadMaterializedAuto(r, size)
			if err != nil {
				return
			}
			metadata, err := InspectTable(r, size)
			if err != nil {
				t.Fatal(err)
			}
			s := *metadata.MaterializedSchema
			pk, _ := s.validate()
			// Full local traversal and row/metadata reconstruction remains compatible.
			got := &Result{Records: make([]Record, 0)}
			report, err := QueryMaterialized(context.Background(), r, size, s, KeyRange{}, ScanOptions{}, collectScan(got))
			if err != nil || !report.Complete || report.LimitReached || !reflect.DeepEqual(got, want.Result) {
				t.Fatal("unbounded", err)
			}
			reverse, report, err := queryRecords(context.Background(), r, size, s, KeyRange{Reverse: true}, ScanOptions{})
			expected := append([]Record{}, want.Result.Records...)
			slices.Reverse(expected)
			if err != nil || !report.Complete || !reflect.DeepEqual(reverse, expected) {
				t.Fatal("reverse", err)
			}
			n := len(want.Result.Records)
			if n > 0 {
				for _, index := range []int{0, n / 2, n - 1} {
					row := want.Result.Records[index]
					key := recordQueryKey(row, s)
					encoded, err := encodeQueryKey(key, s, pk)
					if err != nil {
						t.Fatal("key encoding", key, err)
					}
					if compareIndexKey(encoded, row.key, s, pk) != 0 {
						t.Fatal("physical key encoding", key)
					}
					actual, p, e := queryRecords(context.Background(), r, size, s, PointKey(key), ScanOptions{})
					if e != nil || !p.Complete || len(actual) != 1 || !reflect.DeepEqual(actual[0], row) {
						t.Fatalf("point %d: rows %d %v", index, len(actual), e)
					}
					points++
				}
				if n > 3 {
					lo, hi := n/4, 3*n/4
					lowKey := recordQueryKey(want.Result.Records[lo], s)
					highKey := recordQueryKey(want.Result.Records[hi], s)
					for _, closed := range []bool{false, true} {
						q := KeyRange{Lower: &KeyBound{lowKey, closed}, Upper: &KeyBound{highKey, closed}, Reverse: !closed, Limit: 5}
						start, end := lo+1, hi
						if closed {
							start, end = lo, hi+1
						}
						expected := append([]Record{}, want.Result.Records[start:end]...)
						if q.Reverse {
							slices.Reverse(expected)
						}
						limited := len(expected) >= 5
						if len(expected) > 5 {
							expected = expected[:5]
						}
						actual, p, e := queryRecords(context.Background(), r, size, s, q, ScanOptions{})
						if e != nil || !p.Complete || p.LimitReached != limited || !reflect.DeepEqual(actual, expected) {
							t.Fatal("bounded/limit", e)
						}
						ranges++
					}
				}
			}
			assets++
			rows += n
		})
	}
	if assets != 484 || rows != 98811 {
		t.Fatal("asset counts", assets, rows)
	}
	t.Logf("%d assets, %d rows, %d point lookups, %d bounded/limited queries", assets, rows, points, ranges)
}

func TestQueryNavigation(t *testing.T) {
	b := scanFixture(t, "testdata/trees/deep_rows.ibd.gz")
	s := loadScanSchema(t, "testdata/trees/deep_rows.json")
	r := bytes.NewReader(b)
	size := int64(len(b))
	full, err := Read(r, size, s)
	if err != nil {
		t.Fatal(err)
	}
	mid := len(full.Records) / 2
	key := recordQueryKey(full.Records[mid], s)
	rows, p, err := queryRecords(context.Background(), r, size, s, PointKey(key), ScanOptions{})
	if err != nil || len(rows) != 1 || p.Pages != uint64(full.Page.Level)+1 || p.Pages >= uint64(len(full.Pages)) {
		t.Fatal("point path not pruned", p, err)
	}
	t.Logf("point: %d clustered pages / full %d, requests=%d", p.Pages, len(full.Pages), p.PageReads)
	// A short range straddles leaf pages; reverse must change physical leaf visit order.
	lo, hi := mid-12, mid+12
	q := KeyRange{Lower: &KeyBound{recordQueryKey(full.Records[lo], s), true}, Upper: &KeyBound{recordQueryKey(full.Records[hi], s), true}}
	for _, rev := range []bool{false, true} {
		q.Reverse = rev
		rows, p, err = queryRecords(context.Background(), r, size, s, q, ScanOptions{})
		expected := append([]Record{}, full.Records[lo:hi+1]...)
		if rev {
			slices.Reverse(expected)
		}
		if err != nil || !reflect.DeepEqual(rows, expected) || p.Pages >= uint64(len(full.Pages))/10 {
			t.Fatal("short range", p, err)
		}
		t.Logf("range reverse=%v: %d rows, %d pages", rev, len(rows), p.Pages)
	}
	// The first/last full range boundary, including empty and impossible integer values.
	first, last := recordQueryKey(full.Records[0], s), recordQueryKey(full.Records[len(full.Records)-1], s)
	for _, q := range []KeyRange{{Lower: &KeyBound{last, false}}, {Upper: &KeyBound{first, false}}, {Lower: &KeyBound{last, true}, Upper: &KeyBound{first, true}}, {Lower: &KeyBound{first, false}, Upper: &KeyBound{first, true}}} {
		rows, p, e := queryRecords(context.Background(), r, size, s, q, ScanOptions{})
		if e != nil || !p.Complete || len(rows) != 0 {
			t.Fatal("empty range", p, e)
		}
	}
	// An unrelated leaf is never read for a point in the middle.
	broken := append([]byte(nil), b...)
	unvisited := full.Records[0].PageNumber
	broken[int(unvisited)*PageSize+200] ^= 1
	rows, p, err = queryRecords(context.Background(), bytes.NewReader(broken), size, s, PointKey(key), ScanOptions{})
	if err != nil || !p.Complete || len(rows) != 1 {
		t.Fatal("read unvisited subtree", err)
	}
	if result, e := Read(bytes.NewReader(broken), size, s); result != nil || !errors.Is(e, ErrCorrupt) {
		t.Fatal("full scan did not see damage", e)
	}
	// Corrupt the selected leaf instead.
	broken = append([]byte(nil), b...)
	broken[int(full.Records[mid].PageNumber)*PageSize+200] ^= 1
	_, p, err = queryRecords(context.Background(), bytes.NewReader(broken), size, s, PointKey(key), ScanOptions{})
	if p.Complete || !errors.Is(err, ErrCorrupt) {
		t.Fatal("visited damage", p, err)
	}
}

func TestQueryPrefixes(t *testing.T) {
	paths, _ := filepath.Glob("testdata/composite/*.ibd.gz")
	other, _ := filepath.Glob("testdata/keys/*.ibd.gz")
	paths = append(paths, other...)
	tests := 0
	for _, path := range paths {
		b := scanFixture(t, path)
		r := bytes.NewReader(b)
		size := int64(len(b))
		m, e := InspectTable(r, size)
		if e != nil {
			t.Fatal(e)
		}
		s := *m.Schema
		pk, _ := s.validate()
		if len(pk) < 2 {
			continue
		}
		full, e := Read(r, size, s)
		if e != nil {
			t.Fatal(e)
		}
		if len(full.Records) == 0 {
			continue
		}
		key := recordQueryKey(full.Records[len(full.Records)/2], s)
		for length := 1; length <= len(pk); length++ {
			prefix := key[:length]
			q := KeyRange{Prefix: prefix}
			selection, e := prepareQuery(q, s, pk)
			if e != nil {
				t.Fatal(e)
			}
			expected := []Record{}
			for _, row := range full.Records {
				if selection.comparePrefix(row.key) == 0 {
					expected = append(expected, row)
				}
			}
			for _, rev := range []bool{false, true} {
				q.Reverse = rev
				actual, p, e := queryRecords(context.Background(), r, size, s, q, ScanOptions{})
				want := append([]Record{}, expected...)
				if rev {
					slices.Reverse(want)
				}
				if e != nil || !p.Complete || !reflect.DeepEqual(actual, want) {
					t.Fatalf("prefix %s length%d reverse%v: %v", path, length, rev, e)
				}
				tests++
			}
		}
	}
	t.Logf("%d composite prefix queries", tests)
}

func TestQueryValidation(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r := bytes.NewReader(b)
	size := int64(len(b))
	ctx := context.Background()
	sink := func(ScanEvent) error { return nil }
	for _, q := range []KeyRange{PointKey(Key{}), PointKey(Key{nil}), PointKey(Key{1.0}), PointKey(Key{json.Number("1.2")}), PointKey(Key{uint64(1) << 63}), PointKey(Key{1, 2}), {Prefix: Key{}}, {Prefix: Key{1}, Lower: &KeyBound{Key{1}, true}}} {
		p, e := Query(ctx, r, size, s, q, ScanOptions{}, sink)
		if p.Complete || p.PageReads != 0 || !errors.Is(e, ErrUnsupported) {
			t.Fatal("invalid query", q, p, e)
		}
	}
	for _, run := range []func() (QueryReport, error){func() (QueryReport, error) { return Query(nil, r, size, s, KeyRange{}, ScanOptions{}, sink) }, func() (QueryReport, error) { return Query(ctx, r, size, s, KeyRange{}, ScanOptions{}, nil) }} {
		p, e := run()
		if e == nil || p.Complete {
			t.Fatal(p, e)
		}
	}
	for _, v := range []any{int8(-3), int(-3), int64(-3), json.Number("-3")} {
		rows, p, e := queryRecords(ctx, r, size, s, PointKey(Key{v}), ScanOptions{})
		if e != nil || !p.Complete || len(rows) != 1 {
			t.Fatal("integer normalization", v, p, e)
		}
	}
}

func TestQueryControl(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "tree_compact_instant")
	r := bytes.NewReader(b)
	size := int64(len(b))
	ctx := context.Background()
	sink := func(ScanEvent) error { return nil }
	if p, e := QueryAuto(ctx, r, size, KeyRange{}, ScanOptions{}, sink); p.Complete || !errors.Is(e, ErrUnsupported) {
		t.Fatal("strict virtual", e)
	}
	p, e := QueryMaterializedAuto(ctx, r, size, KeyRange{Limit: 1}, ScanOptions{}, sink)
	if e != nil || !p.Complete || !p.LimitReached || p.Records != 1 {
		t.Fatal("limit", p, e)
	}
	for _, o := range []ScanOptions{{MaxRows: 1}, {MaxPageReads: 1}, {MaxEntries: 1}, {MaxRowBytes: 1}} {
		p, e := QueryMaterialized(ctx, r, size, s, KeyRange{}, o, sink)
		if p.Complete || !errors.Is(e, ErrLimit) {
			t.Fatal("budget", p, e)
		}
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if p, e := QueryMaterialized(c, r, size, s, KeyRange{}, ScanOptions{}, sink); p.Complete || p.PageReads != 0 || !errors.Is(e, context.Canceled) {
		t.Fatal("pre-cancel", e)
	}
	for _, sentinel := range []error{ErrStopped, io.ErrClosedPipe} {
		p, e := QueryMaterialized(ctx, r, size, s, KeyRange{}, ScanOptions{}, func(event ScanEvent) error {
			if event.Record != nil {
				return fmt.Errorf("consumer: %w", sentinel)
			}
			return nil
		})
		if p.Complete || p.Records != 1 || !errors.Is(e, sentinel) {
			t.Fatal("stop", p, e)
		}
	}
	c, cancel = context.WithCancel(ctx)
	p, e = QueryMaterialized(c, r, size, s, KeyRange{}, ScanOptions{}, func(event ScanEvent) error {
		if event.Record != nil {
			cancel()
		}
		return nil
	})
	if p.Complete || p.Records != 1 || !errors.Is(e, context.Canceled) {
		t.Fatal("cancel", p, e)
	}
}

func TestQuerySQL(t *testing.T) {
	raw, err := os.ReadFile("testdata/query/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct{ Name, SHA256 string }
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for _, c := range manifest.Cases {
		hashes[c.Name] = c.SHA256
	}

	names := []string{"signed_rows", "mixed_rows", "unsigned_rows"}
	cases, rows := 0, 0
	for _, name := range names {
		b := scanFixture(t, "testdata/query/"+name+".ibd.gz")
		r := bytes.NewReader(b)
		size := int64(len(b))
		s := loadScanSchema(t, "testdata/query/"+name+".json")
		if fmt.Sprintf("%x", sha256.Sum256(b)) != hashes[name] {
			t.Fatal("snapshot SHA256")
		}
		metadata, err := InspectTable(r, size)
		if err != nil || !reflect.DeepEqual(metadata.Schema, &s) {
			t.Fatal("explicit/official schema", err)
		}
		full, err := Read(r, size, s)
		if err != nil {
			t.Fatal(err)
		}
		var expectedFull []json.RawMessage
		if err = json.Unmarshal(unzip(t, "testdata/query/"+name+".expected.json.gz"), &expectedFull); err != nil {
			t.Fatal(err)
		}
		if len(full.Records) != len(expectedFull) {
			t.Fatal("full SQL count")
		}
		for i, raw := range expectedFull {
			var values []any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err = d.Decode(&values); err != nil {
				t.Fatal(err)
			}
			if compactCanonical(t, full.Records[i].Values, s) != compactCanonical(t, values, s) {
				t.Fatal("full SQL values", i)
			}
		}
		sdi, err := ReadSDI(r, size)
		if err != nil {
			t.Fatal(err)
		}
		var official []json.RawMessage
		if err = json.Unmarshal(unzip(t, "testdata/query/"+name+".sdi.json.gz"), &official); err != nil {
			t.Fatal(err)
		}
		if len(official) != len(sdi.Records)+1 {
			t.Fatal("SDI count")
		}
		for i, record := range sdi.Records {
			var saved struct {
				Type   uint32
				ID     uint64
				Object json.RawMessage
			}
			if err = json.Unmarshal(official[i+1], &saved); err != nil {
				t.Fatal(err)
			}
			if record.Key != (SDIKey{saved.Type, saved.ID}) || !reflect.DeepEqual(jsonTextValue(t, record.JSON), jsonTextValue(t, saved.Object)) {
				t.Fatal("official SDI")
			}
		}

		var queries []struct {
			Name  string
			Query KeyRange
			SQL   string
			Rows  []json.RawMessage
		}
		dec := json.NewDecoder(bytes.NewReader(unzip(t, "testdata/query/"+name+".queries.json.gz")))
		dec.UseNumber()
		if err := dec.Decode(&queries); err != nil {
			t.Fatal(err)
		}
		for _, c := range queries {
			t.Run(name+"/"+c.Name, func(t *testing.T) {
				actual, p, err := queryRecords(context.Background(), r, size, s, c.Query, ScanOptions{})
				if err != nil || !p.Complete || len(actual) != len(c.Rows) {
					t.Fatalf("%s: count %d vs %d error %v", c.SQL, len(actual), len(c.Rows), err)
				}
				for i, row := range c.Rows {
					var expected []any
					d := json.NewDecoder(bytes.NewReader(row))
					d.UseNumber()
					if err = d.Decode(&expected); err != nil {
						t.Fatal(err)
					}
					if compactCanonical(t, actual[i].Values, s) != compactCanonical(t, expected, s) {
						t.Fatal("SQL value/order", c.SQL, i)
					}
				}
				auto := []Record{}
				ap, err := QueryAuto(context.Background(), r, size, c.Query, ScanOptions{}, func(e ScanEvent) error {
					if e.Record != nil {
						auto = append(auto, *e.Record)
					}
					return nil
				})
				if err != nil || !ap.Complete || !reflect.DeepEqual(auto, actual) {
					t.Fatal("auto", err)
				}
				cases++
				rows += len(c.Rows)
			})
		}
	}
	if cases != 92 {
		t.Fatal("query cases", cases)
	}
	t.Logf("%d direct SQL queries, %d returned rows", cases, rows)
}

func TestQueryNoUnmatchedLOB(t *testing.T) {
	b, s := largeLOBFixture(t, "long_over_limit")
	r := bytes.NewReader(b)
	size := int64(len(b))
	got, p, err := queryRecords(context.Background(), r, size, s, PointKey(Key{2}), ScanOptions{})
	if err != nil || !p.Complete || len(got) != 0 || p.Pages != 1 {
		t.Fatal("unmatched oversized LOB", p, err)
	}
	got, p, err = queryRecords(context.Background(), r, size, s, PointKey(Key{0}), ScanOptions{})
	if p.Complete || len(got) != 0 || !errors.Is(err, ErrUnsupported) {
		t.Fatal("matched LOB type limit", p, err)
	}
}

func TestQueryIOFailure(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "tree_compact_initial")
	r := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 10, err: io.ErrUnexpectedEOF}
	rows, p, err := queryRecords(context.Background(), r, int64(len(b)), s, KeyRange{}, ScanOptions{})
	if p.Complete || len(rows) == 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("late I/O", p, err)
	}
}
