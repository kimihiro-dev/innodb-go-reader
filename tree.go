package innodb

import (
	"fmt"
	"io"
	"strings"
)

// NodePointer is navigation metadata, never a user row. Minimum marks the
// leftmost lower-bound sentinel; its stored Key is not a finite lower bound.
type NodePointer struct {
	PageNumber uint32
	Start      int // includes reserved NULL bitmap
	Offset     int
	End        int
	Header     [5]byte
	Key        any // single decoded value or []any tuple in primary-key order
	ChildPage  uint32
	Minimum    bool
}

// DeletedRecord is evidence from the linked physical record, not a recovered SQL row.
// Raw spans [Start,End) in the source page and is independently owned.
type DeletedRecord struct {
	PageNumber         uint32
	Start, Offset, End int
	Header             [5]byte
	Key                any
	RowID              *uint64
	Transaction        [6]byte
	RollPointer        [7]byte
	Raw                []byte
}
type Result struct {
	DeletedRecords []DeletedRecord
	Page           Page   // root; retained for callers of the first-stage API
	Pages          []Page // depth-first, key order
	Nodes          []NodePointer
	Records        []Record // globally ordered clustered keys, SQL column order
}

// Read scans the whole supported clustered index. The caller owns r and supplies
// trusted supported schema. VIRTUAL columns require ReadMaterialized; no undo recovery.
// Any failure returns nil, never a successful-looking partial table.
// Results are materialized; memory scales with rows, deleted local bytes, and visited pages.
func Read(r io.ReaderAt, size int64, schema Schema) (*Result, error) {
	result := &Result{Records: make([]Record, 0)}
	err := walkTree(r, size, schema, func(e ScanEvent) error {
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
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func walkTree(r io.ReaderAt, size int64, schema Schema, yield func(ScanEvent) error) error {
	return walkSelectedTree(r, size, schema, yield, nil)
}

func walkSelectedTree(r io.ReaderAt, size int64, schema Schema, yield func(ScanEvent) error, query *querySelection) error {
	reverse := query != nil && query.reverse
	if len(schema.VirtualColumns) != 0 {
		return fmt.Errorf("%w: VIRTUAL columns are unmaterialized; use ReadMaterialized explicitly", ErrUnsupported)
	}
	pk, err := schema.validate()
	if err != nil {
		return err
	}
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return err
	}
	if err = checkTablespace(fsp, schema); err != nil {
		return err
	}
	type task struct {
		page      uint32
		level     int
		low, high indexKey
	}
	stack := []task{{page: schema.RootPage, level: -1}}
	seen := make(map[uint32]bool)
	last := make(map[uint16]Page)
	var lastLeafKey indexKey
	if err := scanEntries(r, 1); err != nil {
		return err
	}
	for len(stack) > 0 {
		if err := scanCheck(r); err != nil {
			return err
		}
		t := stack[len(stack)-1]
		stack[len(stack)-1] = task{}
		stack = stack[:len(stack)-1]
		if seen[t.page] {
			return fmt.Errorf("%w: repeated/cyclic child page %d", ErrCorrupt, t.page)
		}
		seen[t.page] = true
		b, err := readPage(r, size, t.page)
		if err != nil {
			return err
		}
		p, err := parseIndex(b, schema)
		if err != nil {
			return err
		}
		if t.level >= 0 && int(p.Level) != t.level {
			return fmt.Errorf("%w: page %d level %d, expected %d", ErrCorrupt, p.Number, p.Level, t.level)
		}
		if t.level < 0 {
			if int64(p.Level) >= size/PageSize {
				return fmt.Errorf("%w: impossible root level", ErrCorrupt)
			}
		}
		previous, exists := last[p.Level]
		linked := !exists
		if exists {
			linked = previous.Next == p.Number && p.Previous == previous.Number
			if reverse {
				linked = previous.Previous == p.Number && p.Next == previous.Number
			}
		}
		if !linked || query == nil && !exists && p.Previous != ^uint32(0) {
			return fmt.Errorf("%w: page %d sibling chain does not match tree order", ErrCorrupt, p.Number)
		}
		last[p.Level] = p
		if err := scanEntries(r, uint64(p.Records)); err != nil {
			return err
		}
		entries, err := decodePage(b, p, schema, pk)
		if err != nil {
			return fmt.Errorf("page %d: %w", p.Number, err)
		}
		if len(entries) == 0 && t.level >= 0 {
			return fmt.Errorf("%w: empty child page", ErrCorrupt)
		}
		if p.Level > 0 && len(entries) == 0 {
			return fmt.Errorf("%w: empty internal page", ErrCorrupt)
		}
		// Validate every local entry against its parent before selecting output.
		for _, e := range entries {
			if p.Level > 0 && e.child == 0 {
				return fmt.Errorf("%w: zero child pointer", ErrCorrupt)
			}
			key := e.order
			if !e.minimum && (t.low != nil && compareIndexKey(key, t.low, schema, pk) < 0 || t.high != nil && compareIndexKey(key, t.high, schema, pk) >= 0) {
				return fmt.Errorf("%w: page %d key outside parent range", ErrCorrupt, p.Number)
			}
		}
		if p.Level == 0 && len(entries) > 0 {
			firstKey, lastKey := entries[0].order, entries[len(entries)-1].order
			if reverse {
				firstKey, lastKey = lastKey, firstKey
			}
			if lastLeafKey != nil {
				cmp := compareIndexKey(lastLeafKey, firstKey, schema, pk)
				if !reverse && cmp >= 0 || reverse && cmp <= 0 {
					return fmt.Errorf("%w: cross-page key order", ErrCorrupt)
				}
			}
			lastLeafKey = lastKey
		}
		first, end := 0, len(entries)
		if query != nil {
			first, end = query.entryRange(entries, p, t.low, t.high)
		}
		eventPage := p
		eventPage.Slots = append([]uint16(nil), p.Slots...)
		if err := yield(ScanEvent{Page: &eventPage}); err != nil {
			return err
		}
		for step := first; step < end; step++ {
			entryIndex := step
			if reverse {
				entryIndex = end - 1 - (step - first)
			}
			e := entries[entryIndex]
			if p.Level == 0 {
				entries[entryIndex] = pageEntry{}
			}
			if err := scanCheck(r); err != nil {
				return err
			}
			if p.Level == 0 {
				if e.deleteMarked {
					for _, i := range pk {
						if schema.Columns[i].Type == "CHAR" {
							e.Values[i] = strings.TrimRight(e.Values[i].(string), " ")
						}
					}
					deleted := DeletedRecord{PageNumber: p.Number, Start: e.Start, Offset: e.Offset, End: e.End, Header: e.Header, Key: nodeKey(e.Record, pk), RowID: e.RowID, Transaction: e.Transaction, RollPointer: e.RollPointer, Raw: append([]byte{}, b[e.Start:e.End]...)}
					if err := yield(ScanEvent{DeletedRecord: &deleted}); err != nil {
						return err
					}
					continue
				}
				if query == nil || !query.deferValues {
					if err := scanRow(r, e.Record, schema); err != nil {
						return err
					}
					for i := range e.External {
						field := &e.External[i]
						data, err := readExternal(r, size, field)
						if err != nil {
							return fmt.Errorf("page %d record %d column %q: %w", p.Number, e.Offset, schema.Columns[field.Column].Name, err)
						}
						e.Values[field.Column], err = variableValue(schema.Columns[field.Column], data)
						if err == nil && schema.Columns[field.Column].isText() {
							e.retainTextBytes(field.Column, data)
						}
						if err != nil {
							return err
						}
					}
				}
				for i, c := range schema.Columns {
					if c.Type == "CHAR" && e.Values[i] != nil {
						if e.CharStorage == nil {
							e.CharStorage = make(map[int]string)
						}
						stored := e.Values[i].(string)
						e.CharStorage[i] = stored
						e.Values[i] = strings.TrimRight(stored, " ")
					}
				}

				if err := yield(ScanEvent{Record: &e.Record}); err != nil {
					return err
				}
				if query != nil {
					query.delivered++
					if query.limit != 0 && query.delivered == query.limit {
						query.limitReached = true
						return scanCheck(r)
					}
				}
			} else {
				node := NodePointer{PageNumber: p.Number, Start: e.Start, Offset: e.Offset, End: e.End, Header: e.Header, Key: nodeKey(e.Record, pk), ChildPage: e.child, Minimum: e.minimum}
				if err := yield(ScanEvent{Node: &node}); err != nil {
					return err
				}
			}
		}
		if p.Level > 0 {
			// Push opposite the requested visit direction for this LIFO stack. Only minimum sentinels
			// inherit their lower bound; finite node keys constrain their child.
			for step := end - 1; step >= first; step-- {
				i := step
				if reverse {
					i = first + (end - 1 - step)
				}
				lo, hi := t.low, t.high
				if !entries[i].minimum {
					lo = entries[i].order
				}
				if i+1 < len(entries) {
					hi = entries[i+1].order
				}
				if (lo != nil && hi != nil && compareIndexKey(lo, hi, schema, pk) >= 0) || entries[i].child == 0 {
					return fmt.Errorf("%w: invalid child range/pointer", ErrCorrupt)
				}
				if err := scanEntries(r, 1); err != nil {
					return err
				}
				stack = append(stack, task{entries[i].child, int(p.Level) - 1, lo, hi})
			}
		}
	}
	for _, p := range last {
		if query == nil && p.Next != ^uint32(0) {
			return fmt.Errorf("%w: level %d has unvisited next page %d", ErrCorrupt, p.Level, p.Next)
		}
	}
	return scanCheck(r)
}

func decodeNode(b []byte, origin, limit int, s Schema, pk []int) (pageEntry, error) {
	var e pageEntry
	e.Record = Record{Start: origin - 5, Offset: origin, NextOffset: nextRecord(b, origin), Values: make([]any, len(s.Columns))}
	// Internal records reserve the index's NULL bitmap, but no key is nullable.
	nullable := 0
	for _, c := range s.initialColumns() {
		if c.Nullable {
			nullable++
		}
	}
	e.Start -= (nullable + 7) / 8
	if e.Start < dataStart {
		return e, fmt.Errorf("%w: internal NULL bitmap outside heap", ErrCorrupt)
	}
	for _, v := range b[e.Start : origin-5] {
		if v != 0 {
			return e, fmt.Errorf("%w: internal NULL bitmap is nonzero", ErrCorrupt)
		}
	}
	copy(e.Header[:], b[origin-5:origin])
	flags := e.Header[0] & 0xf0
	if flags&^byte(0x10) != 0 {
		return e, fmt.Errorf("%w: internal record flags %#x", ErrUnsupported, flags)
	}
	if be.Uint16(e.Header[1:3])&7 != 1 {
		return e, fmt.Errorf("%w: expected node-pointer record", ErrCorrupt)
	}
	e.HeapNumber = be.Uint16(e.Header[1:3]) >> 3
	e.minimum = flags == 0x10
	lengths := make([]int, len(pk))
	lengthPos := e.Start - 1
	for i, column := range pk {
		c := s.Columns[column]
		lengths[i] = c.fixedWidth()
		if c.isVariable() {
			n, next, ext, err := readVariableLength(b, lengthPos, c.variableMaxBytes(), false)
			if err != nil {
				return e, err
			}
			if ext {
				return e, fmt.Errorf("%w: external node key", ErrUnsupported)
			}
			lengths[i], lengthPos = n, next
		}
	}
	e.Start = lengthPos + 1
	pos := origin
	for i, column := range pk {
		c := s.Columns[column]
		n := lengths[i]
		if n > limit-pos {
			return e, fmt.Errorf("%w: truncated node key", ErrCorrupt)
		}
		raw := b[pos : pos+n]
		value, err := decodeKeyValue(c, raw)
		if err != nil {
			return e, err
		}
		if c.Type == "CHAR" {
			value = strings.TrimRight(value.(string), " ")
		}
		e.Values[column] = value
		e.key = append(e.key, append([]byte{}, raw...))
		pos += n
	}
	if s.hiddenRowID() {
		if limit-pos < 6 {
			return e, fmt.Errorf("%w: truncated ROW_ID", ErrCorrupt)
		}
		raw := b[pos : pos+6]
		value := rowIDValue(raw)
		e.RowID = &value
		e.key = indexKey{append([]byte{}, raw...)}
		pos += 6
	}

	if limit-pos < 4 {
		return e, fmt.Errorf("%w: truncated child pointer", ErrCorrupt)
	}
	e.child = be.Uint32(b[pos : pos+4])
	e.End = pos + 4
	return e, nil
}

func nodeKey(r Record, pk []int) any {
	if r.RowID != nil {
		return *r.RowID
	}
	return exposedKey(r.Values, pk)
}
