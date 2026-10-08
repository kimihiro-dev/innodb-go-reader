package innodb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func secondaryQueryFixture(t testing.TB, dir, name, index string) ([]byte, Schema, SecondarySchema) {
	t.Helper()
	b := scanFixture(t, "testdata/"+dir+"/"+name+".ibd.gz")
	m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := InspectSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		t.Fatal(err)
	}
	return b, *m.MaterializedSchema, *s
}
func TestSecondaryQueryNavigation(t *testing.T) {
	b, table, index := secondaryQueryFixture(t, "secondary", "deep", "b_idx")
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		t.Fatal(err)
	}
	r := full.Records[len(full.Records)/2]
	key := Key(r.Values[:index.UserFields])
	q := SecondaryQuery{Range: PointKey(key), Columns: []string{"id"}}
	got, p, err := projectedQuery(t, b, table, index, q)
	if err != nil || !p.Complete || p.Lookups != 0 || p.ClusteredPages != 0 || p.Covered != uint64(len(got)) || p.SecondaryPages > 5 || len(got) == 0 {
		t.Fatal(p, err)
	}
	t.Logf("covered point: %d rows, %d secondary pages vs full %d, %d physical reads", len(got), p.SecondaryPages, len(full.Pages), p.PhysicalReads)
	x := append([]byte{}, b...)
	last := full.Records[len(full.Records)-1].PageNumber
	x[int(last)*PageSize+200] ^= 1
	if _, _, err = projectedQuery(t, x, table, index, q); err != nil {
		t.Fatal("unvisited leaf", err)
	}
	if _, err = ReadSecondary(bytes.NewReader(x), int64(len(x)), index); !errors.Is(err, ErrCorrupt) {
		t.Fatal("full corruption", err)
	}
	q.Range.Reverse = true
	q.Range.Limit = 1
	_, rev, err := projectedQuery(t, b, table, index, q)
	if err != nil || !rev.LimitReached || rev.Records != 1 || rev.SecondaryPages > 5 {
		t.Fatal(rev, err)
	}
	// Force a projection lookup without materializing an oversized unrelated LOB.
	b, table, index = secondaryQueryFixture(t, "secondary_query", "huge", "s")
	q = SecondaryQuery{Range: PointKey(Key{1}), Columns: []string{"marker"}}
	got, p, err = projectedQuery(t, b, table, index, q)
	if err != nil || !reflect.DeepEqual(got[0].Values, []any{int32(17)}) || p.Lookups != 1 || p.Covered != 0 {
		t.Fatal(p, err)
	}
	q.Columns = []string{"payload"}
	if _, p, err = projectedQuery(t, b, table, index, q); !errors.Is(err, ErrUnsupported) || p.Complete {
		t.Fatal("selected large LOB", p, err)
	}
	q.Range = PointKey(Key{2})
	got, p, err = projectedQuery(t, b, table, index, q)
	if err != nil || !bytes.Equal(got[0].Values[0].([]byte), []byte("ab")) {
		t.Fatal(p, err)
	}
}
func TestSecondaryQueryLookupDamage(t *testing.T) {
	b, table, index := secondaryQueryFixture(t, "secondary_query", "covering", "s")
	full, err := Read(bytes.NewReader(b), int64(len(b)), table)
	if err != nil {
		t.Fatal(err)
	}
	var target Record
	for _, r := range full.Records {
		if r.Values[1] == int32(499) {
			target = r
			break
		}
	}
	if target.Offset == 0 {
		t.Fatal("target")
	}
	q := SecondaryQuery{Range: PointKey(Key{499}), Columns: []string{"payload"}}
	// n follows BIGINT PK plus 13 system bytes. Inconsistent row remains locally valid.
	x := append([]byte{}, b...)
	pos := int(target.PageNumber)*PageSize + target.Offset + 8 + 13
	copy(x[pos:pos+4], []byte{0x80, 0, 0, 100})
	resealTestPages(x)
	if _, p, err := projectedQuery(t, x, table, index, q); !errors.Is(err, ErrCorrupt) || p.Complete {
		t.Fatal("value mismatch", p, err)
	}
	x = append([]byte{}, b...)
	x[int(target.PageNumber)*PageSize+target.Offset-5] |= 0x20
	resealTestPages(x)
	if _, p, err := projectedQuery(t, x, table, index, q); !errors.Is(err, ErrCorrupt) || p.Complete {
		t.Fatal("missing current locator", p, err)
	}
	q.Columns = []string{"id", "n"}
	if _, p, err := projectedQuery(t, x, table, index, q); err != nil || !p.Complete || p.Lookups != 0 {
		t.Fatal("covered does not certify cluster", p, err)
	}
}
func TestSecondaryQueryControl(t *testing.T) {
	b, table, index := secondaryQueryFixture(t, "secondary_query", "prefixes", "s")
	size := int64(len(b))
	q := SecondaryQuery{Range: KeyRange{Prefix: Key{"abc000"}}, Columns: []string{"id"}}
	rows, p, err := projectedQuery(t, b, table, index, q)
	if err != nil || !p.Complete || len(rows) == 0 || p.Filtered == 0 || p.Lookups <= p.Records {
		t.Fatal("prefix collision", p, err)
	}
	t.Logf("prefix: %d candidates, %d rejected, %d rows, %d lookups", p.Candidates, p.Filtered, p.Records, p.Lookups)
	q.Range.Limit = 1
	got, p, err := projectedQuery(t, b, table, index, q)
	if err != nil || !p.LimitReached || len(got) != 1 || !reflect.DeepEqual(got[0], rows[0]) {
		t.Fatal(p, err)
	}
	options := []ScanOptions{{MaxRows: 1}, {MaxPageReads: 2}, {MaxEntries: 1}, {MaxRowBytes: 1}}
	for _, o := range options {
		_, err := QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), size, table, index, SecondaryQuery{}, o, func(ProjectedRow) error { return nil })
		if !errors.Is(err, ErrLimit) {
			t.Fatal(o, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err = QuerySecondaryMaterialized(ctx, bytes.NewReader(b), size, table, index, SecondaryQuery{}, ScanOptions{}, func(ProjectedRow) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || p.Complete || p.Records != 1 {
		t.Fatal(p, err)
	}
	marker := errors.New("callback")
	for _, e := range []error{marker, ErrStopped} {
		p, err = QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), size, table, index, SecondaryQuery{}, ScanOptions{}, func(ProjectedRow) error { return e })
		if !errors.Is(err, e) || p.Complete || p.Records != 1 {
			t.Fatal(p, err)
		}
	}
	fail := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 12, err: io.ErrUnexpectedEOF}
	p, err = QuerySecondaryMaterialized(context.Background(), fail, size, table, index, SecondaryQuery{}, ScanOptions{}, func(ProjectedRow) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) || p.Complete {
		t.Fatal(p, err)
	}
	// Cumulative entries must not reset at each clustered lookup.
	b, table, index = secondaryQueryFixture(t, "secondary_query", "covering", "s")
	p, err = QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), int64(len(b)), table, index, SecondaryQuery{Columns: []string{"payload"}}, ScanOptions{}, func(ProjectedRow) error { return nil })
	if !errors.Is(err, ErrLimit) || p.Complete || p.Records == 0 || p.TraversalEntries > 1_000_000 {
		t.Fatal("shared entry budget", p, err)
	}
}
func TestSecondaryQueryValidationAndViews(t *testing.T) {
	b, table, index := secondaryQueryFixture(t, "secondary", "generated", "g_idx")
	size := int64(len(b))
	q := SecondaryQuery{Range: PointKey(Key{2}), Columns: []string{"g", "added"}}
	if _, err := QuerySecondaryAuto(context.Background(), bytes.NewReader(b), size, "g_idx", q, ScanOptions{}, func(ProjectedRow) error { return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	p, err := QuerySecondaryMaterializedAuto(context.Background(), bytes.NewReader(b), size, "g_idx", q, ScanOptions{}, func(r ProjectedRow) error {
		if !reflect.DeepEqual(r.Values, []any{int32(2), int32(7)}) {
			t.Fatal(r.Values)
		}
		return nil
	})
	if err != nil || !p.Complete || len(p.VirtualColumns) != 1 {
		t.Fatal(p, err)
	}
	for _, q := range []SecondaryQuery{{Columns: []string{}}, {Columns: []string{"v"}}, {Columns: []string{"g", "g"}}, {Range: PointKey(Key{})}, {Range: PointKey(Key{float64(1)})}, {Range: KeyRange{Prefix: Key{2}, Lower: &KeyBound{Key: Key{2}}}}} {
		if _, _, err := projectedQuery(t, b, table, index, q); !errors.Is(err, ErrUnsupported) {
			t.Fatal(q, err)
		}
	}
	wrong := index
	wrong.SpaceID++
	if _, _, err := projectedQuery(t, b, table, wrong, SecondaryQuery{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), size, table, index, SecondaryQuery{}, ScanOptions{}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
func FuzzSecondaryQueryRange(f *testing.F) {
	b, table, index := secondaryQueryFixture(f, "secondary", "tiny", "n_idx")
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(int8(-1), int8(3), uint8(3), uint8(5))
	f.Add(int8(0), int8(4), uint8(4), uint8(0))
	f.Fuzz(func(t *testing.T, low, high int8, flags, limit uint8) {
		lc, uc, reverse := flags&1 != 0, flags&2 != 0, flags&4 != 0
		q := SecondaryQuery{Range: KeyRange{Lower: &KeyBound{Key: Key{low}, Inclusive: lc}, Upper: &KeyBound{Key: Key{high}, Inclusive: uc}, Reverse: reverse, Limit: uint64(limit)}, Columns: []string{"id"}}
		expected := []int8{}
		for _, r := range full.Records {
			if r.Values[0] == nil {
				continue
			}
			v := r.Values[0].(int8)
			if (v > low || lc && v == low) && (v < high || uc && v == high) {
				expected = append(expected, r.Values[1].(int8))
			}
		}
		if reverse {
			slices.Reverse(expected)
		}
		if limit != 0 && len(expected) > int(limit) {
			expected = expected[:limit]
		}
		got, p, err := projectedQuery(t, b, table, index, q)
		if err != nil || !p.Complete {
			t.Fatal(err)
		}
		actual := []int8{}
		for _, r := range got {
			actual = append(actual, r.Values[0].(int8))
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("range mismatch")
		}
	})
}

func TestSecondaryQueryIndexFilterAndOwnership(t *testing.T) {
	b, table, index := secondaryQueryFixture(t, "secondary", "overlap", "prefix_idx")
	q := SecondaryQuery{Range: PointKey(Key{"ab00050"}), Columns: []string{"payload"}}
	got, p, err := projectedQuery(t, b, table, index, q)
	if err != nil || len(got) != 1 || p.Lookups != 1 || p.Filtered != 119 {
		t.Fatal("full locator copy filters before lookup", p, err)
	}
	b, table, index = secondaryQueryFixture(t, "secondary", "compact", "b_idx")
	full, err := ReadMaterialized(bytes.NewReader(b), int64(len(b)), table)
	if err != nil {
		t.Fatal(err)
	}
	key := Key{append([]byte{}, full.Result.Records[0].Values[3].([]byte)...), append([]byte{}, full.Result.Records[0].Values[4].([]byte)...)}
	q = SecondaryQuery{Range: PointKey(key), Columns: []string{"b", "f"}}
	total := 0
	p, err = QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), int64(len(b)), table, index, q, ScanOptions{}, func(r ProjectedRow) error {
		total++
		for _, v := range r.Values {
			clear(v.([]byte))
		}
		for _, v := range key {
			clear(v.([]byte))
		}
		for i := range r.ClusteredKey {
			r.ClusteredKey[i] = nil
		}
		return nil
	})
	if err != nil || !p.Complete || total != 80 {
		t.Fatal("ownership", p, err)
	}
	// A candidate rejected by the complete prefix predicate must not fetch its LOB.
	b, table, index = secondaryQueryFixture(t, "secondary_query", "prefixes", "s")
	full, err = ReadMaterialized(bytes.NewReader(b), int64(len(b)), table)
	if err != nil {
		t.Fatal(err)
	}
	var badKey Key
	for _, r := range full.Result.Records {
		if len(r.External) > 0 {
			page := r.External[0].FirstPage
			b[int(page)*PageSize+200] ^= 1
			if r.Values[1] != nil {
				badKey = Key{r.Values[1], r.Values[2]}
			}
		}
	}
	q = SecondaryQuery{Range: PointKey(Key{"zzz-NOT-PRESENT", nil}), Columns: []string{"payload"}}
	if rows, p, err := projectedQuery(t, b, table, index, q); err != nil || len(rows) != 0 || p.Filtered == 0 {
		t.Fatal("rejected prefix LOB", p, err)
	}
	q.Range = PointKey(badKey)
	if _, p, err := projectedQuery(t, b, table, index, q); !errors.Is(err, ErrCorrupt) || p.Complete {
		t.Fatal("selected LOB", p, err)
	}
}

func FuzzSecondaryPrefixRanges(f *testing.F) {
	b, table, index := secondaryQueryFixture(f, "secondary_query", "prefixes", "s")
	full, err := ReadMaterialized(bytes.NewReader(b), int64(len(b)), table)
	if err != nil {
		f.Fatal(err)
	}
	secondary, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		f.Fatal(err)
	}
	lookup := map[int32]Record{}
	for _, r := range full.Result.Records {
		lookup[r.Values[0].(int32)] = r
	}
	ordered := []Record{}
	for _, r := range secondary.Records {
		ordered = append(ordered, lookup[r.ClusteredKey[0].(int32)])
	}
	f.Add(uint8(0), uint8(39), int8(8), int8(0), uint8(3), uint8(0))
	f.Add(uint8(8), uint8(24), int8(0), int8(7), uint8(4), uint8(2))
	f.Fuzz(func(t *testing.T, a, z uint8, lowRank, highRank int8, flags, limit uint8) {
		low, high := fmt.Sprintf("abc%03d", a%40), fmt.Sprintf("abc%03d", z%40)
		lc, uc, reverse := flags&1 != 0, flags&2 != 0, flags&4 != 0
		compare := func(r Record, name string, rank int8) int {
			if r.Values[1] == nil {
				return -1
			}
			c := strings.Compare(r.Values[1].(string), name)
			if c != 0 {
				return c
			}
			if r.Values[2] == nil {
				return 1
			}
			v := r.Values[2].(int32)
			if v > int32(rank) {
				return -1
			}
			if v < int32(rank) {
				return 1
			}
			return 0
		}
		expected := []int32{}
		for _, r := range ordered {
			l, u := compare(r, low, lowRank), compare(r, high, highRank)
			if (l > 0 || lc && l == 0) && (u < 0 || uc && u == 0) {
				expected = append(expected, r.Values[0].(int32))
			}
		}
		if reverse {
			slices.Reverse(expected)
		}
		if limit > 0 && len(expected) > int(limit) {
			expected = expected[:limit]
		}
		q := SecondaryQuery{Range: KeyRange{Lower: &KeyBound{Key: Key{low, lowRank}, Inclusive: lc}, Upper: &KeyBound{Key: Key{high, highRank}, Inclusive: uc}, Reverse: reverse, Limit: uint64(limit)}, Columns: []string{"id"}}
		rows, p, err := projectedQuery(t, b, table, index, q)
		if err != nil || !p.Complete {
			t.Fatal(err)
		}
		actual := []int32{}
		for _, r := range rows {
			actual = append(actual, r.Values[0].(int32))
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("prefix interval")
		}
	})
}
