package innodb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

// SecondaryRecord is a physical index entry, never a complete table row. Values
// and FieldBytes follow Schema.Fields; nil means SQL NULL, empty bytes do not.
// Raw covers Start:End, including its header. No transaction columns are invented.
type SecondaryRecord struct {
	PageNumber                     uint32
	Start, Offset, End, NextOffset int
	Header                         [5]byte
	HeapNumber                     uint16
	DeleteMarked                   bool
	Values                         []any
	FieldBytes                     [][]byte
	ClusteredKey                   Key
	RowID                          *uint64
	Raw                            []byte
}

// SecondaryNode contains the complete physical separator key plus its child.
// A Minimum node inherits its parent's lower bound; its value is not a finite bound.
type SecondaryNode struct {
	SecondaryRecord
	ChildPage uint32
	Minimum   bool
}
type SecondaryResult struct {
	Schema                  SecondarySchema
	Page                    Page
	Pages                   []Page
	Nodes                   []SecondaryNode
	Records, DeletedRecords []SecondaryRecord
}

// SecondaryEvent has exactly one non-nil member; events own their data.
type SecondaryEvent struct {
	Page                  *Page
	Node                  *SecondaryNode
	Record, DeletedRecord *SecondaryRecord
}
type SecondaryScanReport struct {
	Schema                                                SecondarySchema
	Complete                                              bool
	Pages, Nodes, Records, DeletedRecords                 uint64
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64
}

// ReadSecondary returns the whole selected index or nil on any failure.
func ReadSecondary(r io.ReaderAt, size int64, schema SecondarySchema) (*SecondaryResult, error) {
	result := &SecondaryResult{Schema: schema, Records: make([]SecondaryRecord, 0)}
	err := walkSecondary(r, size, schema, func(e SecondaryEvent) error {
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
func ReadSecondaryAuto(r io.ReaderAt, size int64, name string) (*SecondaryResult, error) {
	s, err := InspectSecondary(r, size, name)
	if err != nil {
		return nil, err
	}
	return ReadSecondary(r, size, *s)
}
func ScanSecondary(ctx context.Context, r io.ReaderAt, size int64, schema SecondarySchema, options ScanOptions, yield func(SecondaryEvent) error) (SecondaryScanReport, error) {
	return scanSecondary(ctx, r, size, &schema, "", options, yield)
}
func ScanSecondaryAuto(ctx context.Context, r io.ReaderAt, size int64, name string, options ScanOptions, yield func(SecondaryEvent) error) (SecondaryScanReport, error) {
	return scanSecondary(ctx, r, size, nil, name, options, yield)
}
func scanSecondary(ctx context.Context, r io.ReaderAt, size int64, schema *SecondarySchema, name string, options ScanOptions, yield func(SecondaryEvent) error) (report SecondaryScanReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil secondary callback", ErrUnsupported)
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
		schema, err = InspectSecondary(reader, size, name)
		if err != nil {
			return report, err
		}
	}
	report.Schema = *schema
	err = walkSecondary(reader, size, *schema, func(e SecondaryEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case e.Page != nil:
			report.Pages++
		case e.Node != nil:
			report.Nodes++
		case e.Record != nil:
			report.Records++
		case e.DeletedRecord != nil:
			report.DeletedRecords++
		}
		if err := yield(e); err != nil {
			return err
		}
		return ctx.Err()
	})
	report.Complete = err == nil
	return report, err
}

// NULL sorts before non-NULL in ASC; DESC reverses that relationship. Raw keys
// distinguish NULL from a present zero-length value throughout range validation.
func compareSecondary(a, b indexKey, columns []Column) int {
	for i, c := range columns {
		cmp := 0
		switch {
		case a[i] == nil && b[i] != nil:
			cmp = -1
		case a[i] != nil && b[i] == nil:
			cmp = 1
		case a[i] != nil && b[i] != nil:
			cmp = bytes.Compare(a[i], b[i])
			if c.Type == "CHAR" || c.Type == "VARCHAR" {
				cmp = comparePadded(a[i], b[i])
			}
		}
		if c.Descending {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}
func decodeSecondary(b []byte, origin, limit int, columns []Column, fields []SecondaryField, node bool) (pageEntry, error) {
	e := pageEntry{Record: Record{Offset: origin, NextOffset: nextRecord(b, origin), Values: make([]any, len(columns))}}
	copy(e.Header[:], b[origin-5:origin])
	flags := e.Header[0] & 0xf0
	want := uint16(0)
	allowed := byte(0x20)
	if node {
		want = 1
		allowed = 0x10
	}
	if flags & ^allowed != 0 {
		return e, fmt.Errorf("%w: secondary info flags", ErrUnsupported)
	}
	packed := be.Uint16(e.Header[1:3])
	if packed&7 != want {
		return e, fmt.Errorf("%w: secondary record status", ErrCorrupt)
	}
	e.HeapNumber = packed >> 3
	e.minimum = flags&0x10 != 0
	e.deleteMarked = flags&0x20 != 0
	nullable := 0
	for _, c := range columns {
		if c.Nullable {
			nullable++
		}
	}
	nullPos := origin - 6
	lengthPos := nullPos - (nullable+7)/8
	if lengthPos+1 < dataStart {
		return e, fmt.Errorf("%w: secondary NULL bitmap", ErrCorrupt)
	}
	nulls := make([]bool, len(columns))
	lengths := make([]int, len(columns))
	bit := 0
	for i, c := range columns {
		if c.Nullable {
			nulls[i] = b[nullPos-bit/8]&(1<<uint(bit%8)) != 0
			bit++
		}
		if nulls[i] {
			continue
		}
		lengths[i] = c.fixedWidth()
		if c.isVariable() {
			n, next, ext, err := readVariableLength(b, lengthPos, fields[i].Definition.variableMaxBytes(), false)
			if err != nil {
				return e, err
			}
			if ext {
				return e, fmt.Errorf("%w: external secondary field", ErrCorrupt)
			}
			if uint64(n) > c.variableMaxBytes() {
				return e, fmt.Errorf("%w: secondary prefix length", ErrCorrupt)
			}
			lengths[i] = n
			lengthPos = next
		}
	}
	e.Start = lengthPos + 1
	pos := origin
	e.order = make(indexKey, len(columns))
	for i, c := range columns {
		if nulls[i] {
			continue
		}
		n := lengths[i]
		if n < 0 || n > limit-pos {
			return e, fmt.Errorf("%w: secondary field outside heap", ErrCorrupt)
		}
		raw := b[pos : pos+n]
		e.order[i] = append([]byte{}, raw...)
		var v any
		var err error
		if fields[i].RowID {
			v = rowIDValue(raw)
		} else {
			v, err = decodeKeyValue(c, raw)
		}
		if err != nil {
			return e, err
		}
		if c.Type == "CHAR" {
			v = strings.TrimRight(v.(string), " ")
		}
		e.Values[i] = v
		pos += n
	}
	if node {
		if limit-pos < 4 {
			return e, fmt.Errorf("%w: secondary child truncated", ErrCorrupt)
		}
		e.child = be.Uint32(b[pos : pos+4])
		pos += 4
		if e.child == 0 {
			return e, fmt.Errorf("%w: secondary zero child", ErrCorrupt)
		}
	}
	e.End = pos
	return e, nil
}
func secondaryRecord(e pageEntry, b []byte, s SecondarySchema) SecondaryRecord {
	r := SecondaryRecord{PageNumber: e.PageNumber, Start: e.Start, Offset: e.Offset, End: e.End, NextOffset: e.NextOffset, Header: e.Header, HeapNumber: e.HeapNumber, DeleteMarked: e.deleteMarked, Values: e.Values, FieldBytes: make([][]byte, len(e.order)), Raw: append([]byte{}, b[e.Start:e.End]...)}
	for i, raw := range e.order {
		if raw != nil {
			r.FieldBytes[i] = append([]byte{}, raw...)
		}
	}
	for _, i := range s.ClusteredFields {
		v := e.Values[i]
		if raw, ok := v.([]byte); ok {
			v = append([]byte{}, raw...)
		}
		r.ClusteredKey = append(r.ClusteredKey, v)
		if s.Fields[i].RowID {
			n := v.(uint64)
			r.RowID = &n
		}
	}
	return r
}
func walkSecondary(r io.ReaderAt, size int64, s SecondarySchema, yield func(SecondaryEvent) error) error {
	return walkSelectedSecondary(r, size, s, yield, nil)
}

func walkSelectedSecondary(r io.ReaderAt, size int64, s SecondarySchema, yield func(SecondaryEvent) error, selection *secondarySelection) error {
	reverse := selection != nil && selection.reverse
	columns, err := s.validate()
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("%w: nil reader", ErrUnsupported)
	}
	identity := Schema{SpaceID: s.SpaceID, IndexID: s.IndexID, RowFormat: s.RowFormat}
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return err
	}
	if err = checkTablespace(fsp, identity); err != nil {
		return err
	}
	type task struct {
		page      uint32
		level     int
		low, high indexKey
	}
	stack := []task{{page: s.RootPage, level: -1}}
	seen := map[uint32]bool{}
	last := map[uint16]Page{}
	var leaf indexKey
	var unique indexKey
	compare := func(a, b indexKey) int { return compareSecondary(a, b, columns) }
	for len(stack) > 0 {
		if err := scanEntries(r, 1); err != nil {
			return err
		}
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[t.page] {
			return fmt.Errorf("%w: repeated secondary page", ErrCorrupt)
		}
		seen[t.page] = true
		b, err := readPage(r, size, t.page)
		if err != nil {
			return err
		}
		p, err := parseIndexMinimum(b, identity, 6)
		if err != nil {
			return err
		}
		if int64(p.Level) >= size/PageSize || t.level >= 0 && int(p.Level) != t.level {
			return fmt.Errorf("%w: secondary page level", ErrCorrupt)
		}
		previous, ok := last[p.Level]
		linked := previous.Next == p.Number && p.Previous == previous.Number
		if reverse {
			linked = previous.Previous == p.Number && p.Next == previous.Number
		}
		if !ok && selection == nil && p.Previous != ^uint32(0) || ok && !linked {
			return fmt.Errorf("%w: secondary sibling chain", ErrCorrupt)
		}
		last[p.Level] = p
		if err = scanEntries(r, uint64(p.Records)); err != nil {
			return err
		}
		entries, err := decodePageEntries(b, p, func(origin, limit int) (pageEntry, error) {
			return decodeSecondary(b, origin, limit, columns, s.Fields, p.Level > 0)
		}, func(a, b pageEntry) bool { return compare(a.order, b.order) < 0 })
		if err != nil {
			return err
		}
		if len(entries) == 0 && (t.level >= 0 || p.Level > 0) {
			return fmt.Errorf("%w: empty secondary child/internal page", ErrCorrupt)
		}
		for _, e := range entries {
			if !e.minimum && (t.low != nil && compare(e.order, t.low) < 0 || t.high != nil && compare(e.order, t.high) >= 0) {
				return fmt.Errorf("%w: secondary key outside parent range", ErrCorrupt)
			}
		}
		if p.Level == 0 && len(entries) > 0 {
			firstKey, lastKey := entries[0].order, entries[len(entries)-1].order
			if reverse {
				firstKey, lastKey = lastKey, firstKey
			}
			cmp := 0
			if leaf != nil {
				cmp = compare(leaf, firstKey)
			}
			if leaf != nil && (!reverse && cmp >= 0 || reverse && cmp <= 0) {
				return fmt.Errorf("%w: secondary cross-page order", ErrCorrupt)
			}
			leaf = lastKey
		}
		first, end := 0, len(entries)
		if selection != nil {
			first, end = selection.entryRange(entries, p, t.low, t.high)
		}
		eventPage := p
		eventPage.Slots = append([]uint16(nil), p.Slots...)
		if err = yield(SecondaryEvent{Page: &eventPage}); err != nil {
			return err
		}
		for step := first; step < end; step++ {
			i := step
			if reverse {
				i = end - 1 - (step - first)
			}
			e := entries[i]
			if err = scanCheck(r); err != nil {
				return err
			}
			if p.Level == 0 && s.Unique && !e.deleteMarked {
				hasNull := false
				for _, v := range e.order[:s.UserFields] {
					hasNull = hasNull || v == nil
				}
				if !hasNull {
					if unique != nil && compareSecondary(unique, e.order, columns[:s.UserFields]) == 0 {
						return fmt.Errorf("%w: duplicate live unique secondary key", ErrCorrupt)
					}
					unique = e.order
				}
			}
			record := secondaryRecord(e, b, s)
			event := SecondaryEvent{Record: &record}
			if p.Level > 0 {
				event = SecondaryEvent{Node: &SecondaryNode{SecondaryRecord: record, ChildPage: e.child, Minimum: e.minimum}}
			} else if e.deleteMarked {
				event = SecondaryEvent{DeletedRecord: &record}
			} else if selection == nil {
				if err = scanRow(r, e.Record, Schema{}); err != nil {
					return err
				}
			}
			if err = yield(event); err != nil {
				return err
			}
		}
		if p.Level > 0 {
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
				if lo != nil && hi != nil && compare(lo, hi) >= 0 {
					return fmt.Errorf("%w: secondary child bounds", ErrCorrupt)
				}
				if err = scanEntries(r, 1); err != nil {
					return err
				}
				stack = append(stack, task{entries[i].child, int(p.Level) - 1, lo, hi})
			}
		}
	}
	for _, p := range last {
		if selection == nil && p.Next != ^uint32(0) {
			return fmt.Errorf("%w: secondary unvisited sibling", ErrCorrupt)
		}
	}
	return scanCheck(r)
}
