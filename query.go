package innodb

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// KeyBound is a full clustered key endpoint in index order, including each
// member's ASC/DESC direction. Inclusive includes the endpoint's equality class.
type KeyBound struct {
	Key       Key
	Inclusive bool
}

// KeyRange selects either full-key bounds or equality on complete leading columns.
// Prefix is mutually exclusive with Lower/Upper; it is not string prefix matching.
// Nil endpoints are unbounded. Reverse changes output direction, not bounds.
// Limit=0 is unlimited. Inverted bounds and equal open bounds are empty ranges.
type KeyRange struct {
	Lower, Upper *KeyBound
	Prefix       Key
	Reverse      bool
	Limit        uint64
}

// PointKey constructs an exact full-key lookup. Missing or deleted keys yield no
// live Record; absence is a successful query, not an error.
func PointKey(key Key) KeyRange {
	return KeyRange{Lower: &KeyBound{Key: key, Inclusive: true}, Upper: &KeyBound{Key: key, Inclusive: true}}
}

// QueryReport certifies only the requested range/limit on the visited paths, never
// the whole table. LimitReached means the requested count was delivered; it does
// not assert that additional matches exist. Callback failures remain incomplete.
type QueryReport struct {
	ScanReport
	LimitReached bool
}

// Query visits the selected clustered range with trusted schema and complete values.
func Query(ctx context.Context, r io.ReaderAt, size int64, schema Schema, query KeyRange, options ScanOptions, yield func(ScanEvent) error) (QueryReport, error) {
	return queryRows(ctx, r, size, &schema, query, options, yield, false)
}

// QueryAuto discovers the complete-row schema under the same scan budget.
func QueryAuto(ctx context.Context, r io.ReaderAt, size int64, query KeyRange, options ScanOptions, yield func(ScanEvent) error) (QueryReport, error) {
	return queryRows(ctx, r, size, nil, query, options, yield, false)
}

// QueryMaterialized explicitly selects stored columns, separately reporting VIRTUAL.
func QueryMaterialized(ctx context.Context, r io.ReaderAt, size int64, schema Schema, query KeyRange, options ScanOptions, yield func(ScanEvent) error) (QueryReport, error) {
	return queryRows(ctx, r, size, &schema, query, options, yield, true)
}

// QueryMaterializedAuto discovers and queries the stored-column view.
func QueryMaterializedAuto(ctx context.Context, r io.ReaderAt, size int64, query KeyRange, options ScanOptions, yield func(ScanEvent) error) (QueryReport, error) {
	return queryRows(ctx, r, size, nil, query, options, yield, true)
}

func queryRows(ctx context.Context, r io.ReaderAt, size int64, schema *Schema, q KeyRange, options ScanOptions, yield func(ScanEvent) error, materialized bool) (report QueryReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil query callback", ErrUnsupported)
	}
	reader, err := newScanReader(ctx, r, options)
	if err != nil {
		return report, err
	}
	defer func() {
		report.PageReads = reader.requests
		report.CacheHits = reader.hits
		report.PhysicalReads = reader.reads
		report.TraversalEntries = reader.entries
	}()
	if schema == nil {
		m, e := InspectTable(reader, size)
		if e != nil {
			return report, e
		}
		schema = m.Schema
		if materialized {
			schema = m.MaterializedSchema
		}
		if schema == nil {
			return report, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
		}
	}
	s := *schema
	pk, err := s.validate()
	if err != nil {
		return report, err
	}
	if !materialized && len(s.VirtualColumns) > 0 {
		return report, fmt.Errorf("%w: VIRTUAL columns require QueryMaterialized", ErrUnsupported)
	}
	report.Columns = s.Columns
	report.VirtualColumns = s.VirtualColumns
	s.VirtualColumns = nil
	selection, err := prepareQuery(q, s, pk)
	if err != nil {
		return report, err
	}
	if selection.empty {
		err = ctx.Err()
		report.Complete = err == nil
		return report, err
	}
	err = walkSelectedTree(reader, size, s, func(event ScanEvent) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		switch {
		case event.Page != nil:
			report.Pages++
		case event.Node != nil:
			report.Nodes++
		case event.Record != nil:
			report.Records++
		case event.DeletedRecord != nil:
			report.DeletedRecords++
		}
		if e := yield(event); e != nil {
			return e
		}
		return ctx.Err()
	}, selection)
	report.Complete = err == nil
	report.LimitReached = selection.limitReached
	return report, err
}

type querySelection struct {
	deferValues                    bool // Internal secondary lookup: retain references without following LOBs.
	lower, upper, prefix           indexKey
	lowerInclusive, upperInclusive bool
	reverse, empty, limitReached   bool
	limit, delivered               uint64
	schema                         Schema
	pk                             []int
}

func prepareQuery(q KeyRange, s Schema, pk []int) (*querySelection, error) {
	x := &querySelection{schema: s, pk: pk, reverse: q.Reverse, limit: q.Limit}
	if q.Prefix != nil && (q.Lower != nil || q.Upper != nil) {
		return nil, fmt.Errorf("%w: prefix and key bounds are mutually exclusive", ErrUnsupported)
	}
	var err error
	if q.Prefix != nil {
		x.prefix, err = encodeQueryKey(q.Prefix, s, pk)
		if err != nil {
			return nil, err
		}
	}
	count := len(pk)
	if s.hiddenRowID() {
		count = 1
	}
	for _, bound := range []struct {
		src       *KeyBound
		dest      *indexKey
		inclusive *bool
	}{{q.Lower, &x.lower, &x.lowerInclusive}, {q.Upper, &x.upper, &x.upperInclusive}} {
		if bound.src == nil {
			continue
		}
		if len(bound.src.Key) != count {
			return nil, fmt.Errorf("%w: range endpoint must be a complete key", ErrUnsupported)
		}
		*bound.dest, err = encodeQueryKey(bound.src.Key, s, pk)
		if err != nil {
			return nil, err
		}
		*bound.inclusive = bound.src.Inclusive
	}
	if x.lower != nil && x.upper != nil {
		cmp := x.compare(x.lower, x.upper)
		x.empty = cmp > 0 || cmp == 0 && (!x.lowerInclusive || !x.upperInclusive)
	}
	return x, nil
}
func (x *querySelection) compare(a, b indexKey) int { return compareIndexKey(a, b, x.schema, x.pk) }
func (x *querySelection) comparePrefix(key indexKey) int {
	pk := x.pk
	if !x.schema.hiddenRowID() {
		pk = pk[:len(x.prefix)]
	}
	return compareIndexKey(key[:len(x.prefix)], x.prefix, x.schema, pk)
}
func (x *querySelection) below(key indexKey) bool {
	if x.prefix != nil {
		return x.comparePrefix(key) < 0
	}
	if x.lower == nil {
		return false
	}
	cmp := x.compare(key, x.lower)
	return cmp < 0 || cmp == 0 && !x.lowerInclusive
}
func (x *querySelection) above(key indexKey) bool {
	if x.prefix != nil {
		return x.comparePrefix(key) > 0
	}
	if x.upper == nil {
		return false
	}
	cmp := x.compare(key, x.upper)
	return cmp > 0 || cmp == 0 && !x.upperInclusive
}

// hi is a child's exclusive physical upper boundary. A partial-prefix equality
// can still contain matches below hi, so conservatively keep that boundary child.
func (x *querySelection) reachesLower(hi indexKey) bool {
	if hi == nil {
		return true
	}
	if x.prefix != nil {
		cmp := x.comparePrefix(hi)
		return cmp > 0 || cmp == 0 && len(x.prefix) < len(hi)
	}
	return x.lower == nil || x.compare(hi, x.lower) > 0
}
func (x *querySelection) entryRange(entries []pageEntry, p Page, low, high indexKey) (int, int) {
	if p.Level == 0 {
		first := directorySearch(entries, p, func(i int) bool { return !x.below(entries[i].order) })
		end := directorySearch(entries, p, func(i int) bool { return x.above(entries[i].order) })
		return first, end
	}
	first := directorySearch(entries, p, func(i int) bool {
		hi := high
		if i+1 < len(entries) {
			hi = entries[i+1].order
		}
		return x.reachesLower(hi)
	})
	end := directorySearch(entries, p, func(i int) bool {
		lo := low
		if !entries[i].minimum {
			lo = entries[i].order
		}
		return lo != nil && x.above(lo)
	})
	return first, end
}

// Search directory owner groups, then the entries owned by the
// selected slot. decodePage has already validated directory ownership and order.
func directorySearch(entries []pageEntry, p Page, predicate func(int) bool) int {
	if len(entries) == 0 {
		return 0
	}
	positions := make(map[int]int, len(entries))
	for i, e := range entries {
		positions[e.Offset] = i
	}
	ends := make([]int, 0, len(p.Slots)-1)
	for _, slot := range p.Slots[1:] {
		if slot == supremum {
			ends = append(ends, len(entries)-1)
		} else {
			ends = append(ends, positions[int(slot)])
		}
	}
	group := sort.Search(len(ends), func(i int) bool { return predicate(ends[i]) })
	if group == len(ends) {
		return len(entries)
	}
	start := 0
	if group > 0 {
		start = ends[group-1] + 1
	}
	end := ends[group] + 1
	return start + sort.Search(end-start, func(i int) bool { return predicate(start + i) })
}
