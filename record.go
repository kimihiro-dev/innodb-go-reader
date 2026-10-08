package innodb

import (
	"fmt"
	"slices"
	"unicode/utf8"
)

// Record exposes values in Schema.Columns order. Values are typed integers, float32/float64, string, []byte, JSONValue, GeometryValue, or nil; DECIMAL uses an exact fixed-scale string; see docs/STAGE_PLANS.md#stage-7.
// Offsets are page-relative. Start includes variable metadata; Offset is the origin
// (first clustered key byte, including hidden ROW_ID); End is exclusive. The transaction fields are not a ReadView.
type Record struct {
	RowVersion     *uint8 // Explicit row-version byte; nil means an unversioned record.
	DefaultColumns []int  // Logical columns synthesized from instant ADD defaults.
	deleteMarked   bool
	RowID          *uint64 // Hidden DB_ROW_ID only; nil for user-column clustered keys.
	key            indexKey
	PageNumber     uint32
	Start          int
	Offset         int
	End            int
	Header         [5]byte
	HeapNumber     uint16
	NextOffset     int
	Transaction    [6]byte
	RollPointer    [7]byte
	Values         []any
	TextBytes      map[int][]byte  // non-NULL CHAR/VARCHAR/TEXT original bytes, including CHAR padding
	CharStorage    map[int]string  // non-NULL CHAR physical text, including residual padding
	SetMasks       map[int]uint64  // non-NULL SET masks, keyed by logical column index
	EnumIndexes    map[int]uint16  // non-NULL ENUM ordinals, keyed by logical column index
	External       []ExternalField // physical references and resolved LOB chunk provenance
}

// pageEntry is either an ordinary leaf record or a node-pointer record.
type pageEntry struct {
	Record
	child   uint32
	minimum bool
	order   indexKey
	sdiKey  SDIKey
}

func decodePage(b []byte, p Page, schema Schema, pk []int) ([]pageEntry, error) {
	return decodePageEntries(b, p, func(origin, limit int) (pageEntry, error) {
		var e pageEntry
		var err error
		if p.Level == 0 {
			e.Record, err = decodeRecord(b, origin, limit, schema, pk)
		} else {
			e, err = decodeNode(b, origin, limit, schema, pk)
		}
		if err == nil {
			e.order = e.key
		}
		return e, err
	}, func(a, b pageEntry) bool { return compareIndexKey(a.order, b.order, schema, pk) < 0 })
}

func decodePageEntries(b []byte, p Page, decode func(int, int) (pageEntry, error), less func(pageEntry, pageEntry) bool) ([]pageEntry, error) {
	if string(b[99:107]) != "infimum\x00" || string(b[112:120]) != "supremum" ||
		be.Uint16(b[95:97]) != 2 || be.Uint16(b[108:110]) != 11 ||
		b[94] != 1 || b[107]&0xf0 != 0 || nextRecord(b, supremum) != 0 {
		return nil, fmt.Errorf("%w: invalid system records", ErrCorrupt)
	}
	result := make([]pageEntry, 0, p.Records)
	seen := make(map[int]bool)
	heaps := make(map[uint16]bool)
	occupied := make([]bool, p.HeapTop)
	// Match directory ownership as records are visited in key order.
	slot, owned := 1, 0
	liveBytes := 0
	for origin := nextRecord(b, infimum); ; {
		owned++
		if origin == supremum {
			if slot != len(p.Slots)-1 || int(b[supremum-5]&15) != owned {
				return nil, fmt.Errorf("%w: supremum directory ownership mismatch", ErrCorrupt)
			}
			break
		}
		if origin < dataStart+5 || origin >= int(p.HeapTop) || seen[origin] || len(result) >= int(p.Records) {
			return nil, fmt.Errorf("%w: invalid/repeated record origin %d", ErrCorrupt, origin)
		}
		seen[origin] = true
		entry, err := decode(origin, int(p.HeapTop))
		if p.Level > 0 {
			wantMinimum := len(result) == 0 && p.Previous == ^uint32(0)
			if err == nil && entry.minimum != wantMinimum {
				err = fmt.Errorf("%w: incorrect minimum-record flag", ErrCorrupt)
			}
		}
		rec := entry.Record
		rec.PageNumber = p.Number
		entry.Record = rec
		if err != nil {
			return nil, fmt.Errorf("page %d record %d: %w", p.Number, origin, err)
		}
		if rec.HeapNumber < 2 || rec.HeapNumber >= p.HeapCount || heaps[rec.HeapNumber] {
			return nil, fmt.Errorf("%w: invalid/repeated heap number %d", ErrCorrupt, rec.HeapNumber)
		}
		heaps[rec.HeapNumber] = true
		for i := rec.Start; i < rec.End; i++ {
			if occupied[i] {
				return nil, fmt.Errorf("%w: overlapping record at byte %d", ErrCorrupt, i)
			}
			occupied[i] = true
		}
		if n := len(result); n > 0 && !result[n-1].minimum && !less(result[n-1], entry) {
			return nil, fmt.Errorf("%w: primary keys are not strictly increasing", ErrCorrupt)
		}
		if origin == int(p.Slots[slot]) {
			if int(rec.Header[0]&15) != owned {
				return nil, fmt.Errorf("%w: directory ownership mismatch", ErrCorrupt)
			}
			slot++
			owned = 0
		} else if rec.Header[0]&15 != 0 {
			return nil, fmt.Errorf("%w: unexpected directory owner", ErrCorrupt)
		}
		liveBytes += rec.End - rec.Start
		result = append(result, entry)
		origin = rec.NextOffset
	}
	if len(result) != int(p.Records) {
		return nil, fmt.Errorf("%w: decoded %d records, header says %d", ErrCorrupt, len(result), p.Records)
	}
	// Reusing a larger free record leaves fragments even when PAGE_FREE is 0.
	// page_get_data_size: heap bytes minus garbage equal active record bytes.
	if liveBytes != int(p.HeapTop)-dataStart-int(p.Garbage) {
		return nil, fmt.Errorf("%w: live record bytes do not match heap/garbage accounting", ErrCorrupt)
	}
	// Splits leave discarded records on PAGE_FREE even without SQL DELETE.
	// Validate that this separate chain cannot re-enter live records. Its values
	// are not decoded and must never be returned as rows.
	for origin := int(p.Free); origin != 0; origin = nextRecord(b, origin) {
		if origin < dataStart+5 || origin >= int(p.HeapTop) || seen[origin] {
			return nil, fmt.Errorf("%w: invalid/cyclic free record %d", ErrCorrupt, origin)
		}
		seen[origin] = true
		heap := be.Uint16(b[origin-4:origin-2]) >> 3
		if heap < 2 || heap >= p.HeapCount || heaps[heap] {
			return nil, fmt.Errorf("%w: invalid/repeated free heap number", ErrCorrupt)
		}
		heaps[heap] = true
		for i := origin - 5; i < origin; i++ {
			if occupied[i] {
				return nil, fmt.Errorf("%w: free header overlaps another record", ErrCorrupt)
			}
			occupied[i] = true
		}
	}
	if len(heaps)+2 != int(p.HeapCount) {
		return nil, fmt.Errorf("%w: heap records missing from live/free chains", ErrCorrupt)
	}
	return result, nil
}

// rem0rec.ic rec_get_next_offs: compact offsets are relative, wrapping within page.
func nextRecord(b []byte, origin int) int {
	delta := int(be.Uint16(b[origin-2 : origin]))
	if delta == 0 {
		return 0
	}
	return (origin + delta) & (PageSize - 1)
}

func decodeRecord(b []byte, origin, limit int, s Schema, pk []int) (Record, error) {
	if s.Instant != nil {
		return decodeInstantRecord(b, origin, limit, s, pk)
	}
	return decodeStoredRecord(b, origin, limit, s, pk, 0, nil)
}

func decodeStoredRecord(b []byte, origin, limit int, s Schema, pk []int, versionBytes int, dropped []bool) (Record, error) {
	r := Record{Offset: origin, NextOffset: nextRecord(b, origin), Values: make([]any, len(s.Columns))}
	copy(r.Header[:], b[origin-5:origin])
	r.deleteMarked = r.Header[0]&0x20 != 0
	mask := byte(0xd0)
	if versionBytes == 1 {
		mask = 0x90
	}
	if r.Header[0]&mask != 0 {
		return r, fmt.Errorf("%w: record info flags %#x (instant/version/min)", ErrUnsupported, r.Header[0]&0xf0)
	}
	packed := be.Uint16(r.Header[1:3])
	r.HeapNumber = packed >> 3
	if packed&7 != 0 {
		return r, fmt.Errorf("%w: expected ordinary leaf record", ErrUnsupported)
	}
	nullCount := 0
	for _, c := range s.Columns {
		if c.Nullable {
			nullCount++
		}
	}
	nullPos := origin - 6 - versionBytes
	lengthPos := nullPos - (nullCount+7)/8
	if lengthPos+1 < dataStart {
		return r, fmt.Errorf("%w: NULL bitmap outside record heap", ErrCorrupt)
	}
	nulls := make([]bool, len(s.Columns))
	lengths := make([]int, len(s.Columns))
	external := make([]bool, len(s.Columns))
	bit := 0
	// Length metadata follows physical field order: keys, then remaining columns.
	physical := append([]int{}, pk...)
	for i := range s.Columns {
		if !slices.Contains(pk, i) {
			physical = append(physical, i)
		}
	}
	for _, i := range physical {
		c := s.Columns[i]
		if c.Nullable {
			nulls[i] = b[nullPos-bit/8]&(1<<uint(bit%8)) != 0
			bit++
		}
		if maximum := c.variableMaxBytes(); c.isVariable() && !nulls[i] {
			length, next, ext, err := readVariableLength(b, lengthPos, maximum, c.lobTypeMaxBytes() != 0)
			if err != nil {
				return r, fmt.Errorf("column %q: %w", c.Name, err)
			}
			lengths[i], lengthPos, external[i] = length, next, ext
		}
	}
	r.Start = lengthPos + 1
	pos := origin
	take := func(n int) ([]byte, error) {
		if n > limit-pos {
			return nil, fmt.Errorf("%w: field extends beyond heap at %d", ErrCorrupt, pos)
		}
		value := b[pos : pos+n]
		pos += n
		return value, nil
	}
	for _, column := range pk {
		c := s.Columns[column]
		n := c.fixedWidth()
		if c.isVariable() {
			n = lengths[column]
		}
		if external[column] {
			return r, fmt.Errorf("%w: external primary key", ErrUnsupported)
		}
		key, err := take(n)
		if err != nil {
			return r, err
		}
		r.Values[column], err = decodeKeyValue(c, key)
		if err != nil {
			return r, fmt.Errorf("column %q: %w", c.Name, err)
		}
		r.key = append(r.key, append([]byte{}, key...))
		if c.isText() {
			r.retainTextBytes(column, key)
		}
	}

	if s.hiddenRowID() {
		raw, err := take(6)
		if err != nil {
			return r, err
		}
		value := rowIDValue(raw)
		r.RowID = &value
		r.key = indexKey{append([]byte{}, raw...)}
	}

	system, err := take(13)
	if err != nil {
		return r, err
	}
	copy(r.Transaction[:], system[:6])
	copy(r.RollPointer[:], system[6:])
	for i, c := range s.Columns {
		if slices.Contains(pk, i) || nulls[i] {
			continue
		}
		length := c.fixedWidth()
		if c.isVariable() {
			length = lengths[i]
		}
		value, err := take(length)
		if err != nil {
			return r, err
		}
		if r.deleteMarked || dropped != nil && dropped[i] {
			continue
		} // Local evidence only; do not decode historical non-key values.

		if external[i] {
			var field ExternalField
			var err error
			if s.RowFormat == "COMPACT" {
				field, err = parseCompactExternal(value, c.variableMaxBytes(), s.SpaceID)
			} else {
				field, err = parseExternal(value, c.variableMaxBytes(), s.SpaceID)
			}
			if err != nil {
				return r, fmt.Errorf("column %q: %w", c.Name, err)
			}
			field.Column, field.Offset = i, pos-20
			r.External = append(r.External, field)
		} else if err := r.decodeStoredValue(i, c, value); err != nil {
			return r, err
		}
	}
	r.End = pos
	return r, nil
}

// decodeStoredValue also decodes instant ADD defaults in InnoDB field encoding.
func (r *Record) decodeStoredValue(i int, c Column, value []byte) error {
	var err error
	if integerWidth(c.Type) != 0 {
		r.Values[i] = decodeInteger(value, c.Unsigned)
	} else if floatWidth(c.Type) != 0 {
		r.Values[i], err = decodeFloat(value, c)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if dateWidth(c.Type) != 0 {
		r.Values[i], err = decodeDate(value, c.Type)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if c.Type == "SET" {
		label, mask, err := decodeSet(value, c.SetValues)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
		r.Values[i] = label
		if r.SetMasks == nil {
			r.SetMasks = make(map[int]uint64)
		}
		r.SetMasks[i] = mask
	} else if c.Type == "ENUM" {
		label, index, err := decodeEnum(value, c.EnumValues)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
		r.Values[i] = label
		if r.EnumIndexes == nil {
			r.EnumIndexes = make(map[int]uint16)
		}
		r.EnumIndexes[i] = index
	} else if c.Type == "BIT" {
		r.Values[i], err = decodeBit(value, c.BitLength)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if c.Type == "TIMESTAMP" {
		r.Values[i], err = decodeTimestamp(value, c.FSP)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if c.Type == "TIME" {
		r.Values[i], err = decodeTime(value, c.FSP)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if c.Type == "DATETIME" {
		r.Values[i], err = decodeDatetime(value, c.FSP)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else if c.Type == "DECIMAL" {
		r.Values[i], err = decodeDecimal(value, c)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
	} else {
		r.Values[i], err = variableValue(c, value)
		if err == nil && c.isText() {
			r.retainTextBytes(i, value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Validate text only after all external chunks have been concatenated.
func variableValue(c Column, value []byte) (any, error) {
	if geometryType(c.Type) {
		return geometryValue(c, value)
	}
	if c.Type == "JSON" {
		return DecodeJSON(value)
	}
	if c.isBinary() {
		binary := make([]byte, len(value))
		copy(binary, value)
		return binary, nil
	}
	text, err := decodeText(c.charsetName(), value)
	if err != nil {
		return nil, fmt.Errorf("column %q: %w", c.Name, err)
	}
	if (c.Type == "CHAR" || c.Type == "VARCHAR") && utf8.RuneCountInString(text) > c.MaxChars {
		return nil, fmt.Errorf("%w: column %q exceeds character limit", ErrCorrupt, c.Name)
	}
	if c.Type == "CHAR" && (len(value) < c.MaxChars || uint64(len(value)) > c.variableMaxBytes() || len(value) > c.MaxChars && value[len(value)-1] == 0x20) {
		return nil, fmt.Errorf("%w: column %q has invalid CHAR storage length/padding", ErrCorrupt, c.Name)
	}
	return text, nil
}

func (r *Record) retainTextBytes(column int, data []byte) {
	if r.TextBytes == nil {
		r.TextBytes = make(map[int][]byte)
	}
	r.TextBytes[column] = append([]byte{}, data...)
}
