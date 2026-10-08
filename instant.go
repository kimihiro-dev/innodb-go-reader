package innodb

import (
	"encoding/hex"
	"fmt"
	"sort"
)

// InstantLayout describes the native MySQL 8.0.29+ row-version layout.
// Positions include clustered key and system fields; Fields excludes system fields.
type InstantLayout struct {
	Version uint8          `json:"version"`
	Fields  []InstantField `json:"fields"`
}
type InstantField struct {
	Position      int             `json:"position"`
	Column        int             `json:"column"` // current logical column index, or -1 when dropped
	DroppedColumn *Column         `json:"dropped_column,omitempty"`
	Added         uint8           `json:"added,omitempty"`
	Dropped       uint8           `json:"dropped,omitempty"`
	Default       *InstantDefault `json:"default,omitempty"`
}
type InstantDefault struct {
	Null bool   `json:"null,omitempty"`
	Data []byte `json:"data,omitempty"` // InnoDB encoded value, not SQL display text
}

func (s Schema) fieldColumn(f InstantField) Column {
	if f.DroppedColumn != nil {
		return *f.DroppedColumn
	}
	return s.Columns[f.Column]
}
func (f InstantField) present(v uint8) bool { return f.Added <= v && (f.Dropped == 0 || v < f.Dropped) }
func (s Schema) initialColumns() []Column {
	if s.Instant == nil {
		return s.Columns
	}
	var columns []Column
	for _, f := range s.Instant.Fields {
		if f.present(0) {
			columns = append(columns, s.fieldColumn(f))
		}
	}
	return columns
}
func (s Schema) validateInstant(pk []int) error {
	if s.Instant == nil {
		return nil
	}
	x := s.Instant
	bad := func(reason string) error { return fmt.Errorf("%w: instant layout %s", ErrUnsupported, reason) }
	if x.Version == 0 || x.Version > 64 || len(x.Fields) < len(s.Columns) || len(x.Fields) > 1017 {
		return bad("version/field count")
	}
	keyCount := len(pk)
	if s.hiddenRowID() {
		keyCount = 1
	}
	systemCount := 2
	if s.hiddenRowID() {
		systemCount++
	}
	positions := map[int]bool{keyCount: true, keyCount + 1: true}
	if s.hiddenRowID() {
		positions[0] = true
	}
	seen := make(map[int]bool)
	all := s
	all.Instant = nil
	all.Columns = append([]Column{}, s.Columns...)
	maxVersion := uint8(0)
	for _, f := range x.Fields {
		if f.Position < 0 || f.Position >= len(x.Fields)+systemCount || positions[f.Position] {
			return bad("physical position")
		}
		positions[f.Position] = true
		if f.Added > x.Version || f.Dropped > x.Version || f.Dropped != 0 && f.Dropped <= f.Added {
			return bad("column lifetime")
		}
		if f.Added > maxVersion {
			maxVersion = f.Added
		}
		if f.Dropped > maxVersion {
			maxVersion = f.Dropped
		}
		if f.DroppedColumn != nil {
			if f.Column != -1 || f.Dropped == 0 {
				return bad("dropped column definition")
			}
			all.Columns = append(all.Columns, *f.DroppedColumn)
		} else {
			if f.Column < 0 || f.Column >= len(s.Columns) || seen[f.Column] || f.Dropped != 0 {
				return bad("logical column mapping")
			}
			seen[f.Column] = true
			for n, i := range pk {
				if i == f.Column && (f.Position != n || f.Added != 0) {
					return bad("versioned/reordered key")
				}
			}
		}
		c := s.fieldColumn(f)
		if f.Added == 0 && f.Default != nil || f.Added != 0 && f.Dropped == 0 && f.Default == nil {
			return bad("missing/unexpected ADD default")
		}
		if f.Default != nil {
			d := f.Default
			if d.Null && (!c.Nullable || len(d.Data) != 0) {
				return bad("NULL default")
			}
		}
	}
	if len(seen) != len(s.Columns) || maxVersion != x.Version {
		return bad("incomplete columns/version")
	}
	for i := 0; i < len(positions); i++ {
		if !positions[i] {
			return bad("missing physical position")
		}
	}
	// Validate historical type descriptors through the same type/key contract.
	if _, err := all.validate(); err != nil {
		return err
	}
	for _, f := range x.Fields {
		if f.Default != nil && !f.Default.Null {
			c := s.fieldColumn(f)
			if c.isVariable() {
				if uint64(len(f.Default.Data)) > c.variableMaxBytes() {
					return bad("default length")
				}
			} else if len(f.Default.Data) != c.fixedWidth() {
				return bad("default width")
			}
			r := Record{Values: make([]any, 1)}
			if err := r.decodeStoredValue(0, s.fieldColumn(f), f.Default.Data); err != nil {
				return fmt.Errorf("instant default: %w", err)
			}
		}
	}
	return nil
}

func decodeInstantRecord(b []byte, origin, limit int, s Schema, pk []int) (Record, error) {
	var empty Record
	flags := b[origin-5] & 0xf0
	if flags&0x90 != 0 {
		return empty, fmt.Errorf("%w: legacy instant/min record flags", ErrUnsupported)
	}
	version := uint8(0)
	extra := 0
	if flags&0x40 != 0 {
		if origin-6 < dataStart {
			return empty, fmt.Errorf("%w: row version outside heap", ErrCorrupt)
		}
		version = b[origin-6]
		extra = 1
		if version == 0 || version > s.Instant.Version {
			return empty, fmt.Errorf("%w: invalid row version %d", ErrCorrupt, version)
		}
	}
	fields := append([]InstantField{}, s.Instant.Fields...)
	sort.Slice(fields, func(i, j int) bool { return fields[i].Position < fields[j].Position })
	physical := s
	physical.Instant = nil
	physical.Columns = nil
	mapping := []int{}
	dropped := []bool{}
	physicalPK := make([]int, len(pk))
	for _, f := range fields {
		if f.present(version) {
			n := len(physical.Columns)
			physical.Columns = append(physical.Columns, s.fieldColumn(f))
			mapping = append(mapping, f.Column)
			dropped = append(dropped, f.Dropped != 0)
			for k, i := range pk {
				if f.Column == i {
					physicalPK[k] = n
				}
			}
		}
	}
	r, err := decodeStoredRecord(b, origin, limit, physical, physicalPK, extra, dropped)
	if err != nil {
		return empty, err
	}
	if extra == 1 {
		r.RowVersion = &version
	}
	// Decode in physical order, then expose current SQL declaration order.
	result := r
	result.Values = make([]any, len(s.Columns))
	result.TextBytes = nil
	result.CharStorage = nil
	result.EnumIndexes = nil
	result.SetMasks = nil
	result.External = nil
	for i, j := range mapping {
		if j >= 0 {
			result.Values[j] = r.Values[i]
			if v, ok := r.TextBytes[i]; ok {
				result.retainTextBytes(j, v)
			}
			if v, ok := r.EnumIndexes[i]; ok {
				if result.EnumIndexes == nil {
					result.EnumIndexes = map[int]uint16{}
				}
				result.EnumIndexes[j] = v
			}
			if v, ok := r.SetMasks[i]; ok {
				if result.SetMasks == nil {
					result.SetMasks = map[int]uint64{}
				}
				result.SetMasks[j] = v
			}
		}
	}
	for _, f := range r.External {
		f.Column = mapping[f.Column]
		result.External = append(result.External, f)
	}
	if !r.deleteMarked {
		for _, f := range s.Instant.Fields {
			if f.Column >= 0 && f.Added > version {
				result.DefaultColumns = append(result.DefaultColumns, f.Column)
				if !f.Default.Null {
					if err := result.decodeStoredValue(f.Column, s.Columns[f.Column], f.Default.Data); err != nil {
						return empty, err
					}
				}
			}
		}
	}
	sort.Ints(result.DefaultColumns)
	return result, nil
}

func metadataInstant(s *Schema, columns []ddColumn) (string, error) {
	props := make([]map[string]string, len(columns))
	native := false
	for i, c := range columns {
		p, err := metadataProperties(c.Private)
		if err != nil {
			return "", err
		}
		props[i] = p
		for _, key := range []string{"physical_pos", "version_added", "version_dropped", "default", "default_null"} {
			if _, ok := p[key]; ok {
				native = true
			}
		}
	}
	if !native {
		return "", nil
	}
	pk, err := s.validate()
	if err != nil {
		return err.Error(), nil
	}
	keyCount := len(pk)
	if s.hiddenRowID() {
		keyCount = 1
	}
	layout := &InstantLayout{}
	for i, c := range columns {
		if c.Virtual {
			continue
		}
		p := props[i]
		if _, ok := p["physical_pos"]; !ok {
			return "missing INSTANT physical_pos or legacy instant layout", nil
		}
		position, err := propertyUint(p, "physical_pos", 16)
		if err != nil {
			return "", err
		}
		f := InstantField{Position: int(position), Column: -1}
		for _, v := range []struct {
			name string
			to   *uint8
		}{{"version_added", &f.Added}, {"version_dropped", &f.Dropped}} {
			if _, ok := p[v.name]; ok {
				n, err := propertyUint(p, v.name, 8)
				if err != nil {
					return "", err
				}
				if n == 0 || n > 64 {
					return "invalid INSTANT column version", nil
				}
				*v.to = uint8(n)
			}
		}
		raw, hasDefault := p["default"]
		null, hasNull := p["default_null"]
		if hasDefault && hasNull || hasNull && null != "1" {
			return "invalid INSTANT default properties", nil
		}
		if hasDefault {
			data, err := hex.DecodeString(raw)
			if err != nil {
				return "", fmt.Errorf("%w: INSTANT default hex", ErrCorrupt)
			}
			if len(data) == 0 {
				data = nil
			}
			f.Default = &InstantDefault{Data: data}
		}
		if hasNull {
			f.Default = &InstantDefault{Null: true}
		}
		systemPosition := -1
		switch c.Name {
		case "DB_ROW_ID":
			systemPosition = 0
		case "DB_TRX_ID":
			systemPosition = keyCount
		case "DB_ROLL_PTR":
			systemPosition = keyCount + 1
		}
		if c.Hidden == 2 && systemPosition >= 0 {
			if f.Position != systemPosition || f.Added != 0 || f.Dropped != 0 || f.Default != nil {
				return "invalid INSTANT system position/version", nil
			}
			continue
		}
		if f.Dropped != 0 {
			if c.Hidden != 2 {
				return "INSTANT dropped column is not hidden", nil
			}
			converted, reason, err := metadataColumn(c)
			if err != nil {
				return "", err
			}
			if reason != "" {
				return "dropped column: " + reason, nil
			}
			f.DroppedColumn = &converted
		} else {
			for j, user := range s.Columns {
				if user.Name == c.Name {
					f.Column = j
					break
				}
			}
			if f.Column < 0 {
				return "INSTANT column mapping missing", nil
			}
		}
		layout.Version = max(layout.Version, f.Added, f.Dropped)
		layout.Fields = append(layout.Fields, f)
	}
	sort.Slice(layout.Fields, func(i, j int) bool { return layout.Fields[i].Position < layout.Fields[j].Position })
	s.Instant = layout
	return "", nil
}
