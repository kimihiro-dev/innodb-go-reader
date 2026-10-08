package innodb

import (
	"fmt"
	"io"
	"strings"
)

// SecondaryField describes one physical field. Column is a materialized table
// column index, or -1 for ROW_ID. PrefixBytes is a declared byte maximum, not
// proof that this particular value was truncated. Definition retains the source
// column type, with this index member's direction and collation.
type SecondaryField struct {
	Column      int
	Definition  Column
	PrefixBytes int
	RowID       bool
}

// SecondarySchema describes the complete physical key, including any appended
// clustered fields. ClusteredFields maps the clustered key order to Fields.
// UserFields counts the declared secondary fields, not physical uniqueness.
type SecondarySchema struct {
	Name            string
	Unique          bool
	RowFormat       string
	SpaceID         uint32
	IndexID         uint64
	RootPage        uint32
	Fields          []SecondaryField
	UserFields      int
	ClusteredFields []int
}

// InspectSecondary selects a supported ordinary secondary index by exact SDI name.
// Non-indexed VIRTUAL columns are not evaluated; indexed VIRTUAL is unsupported.
func InspectSecondary(r io.ReaderAt, size int64, name string) (*SecondarySchema, error) {
	m, err := InspectTable(r, size)
	if err != nil {
		return nil, err
	}
	return inspectSecondaryMetadata(m, name)
}

func inspectSecondaryMetadata(m *TableMetadata, name string) (*SecondarySchema, error) {
	if m.MaterializedSchema == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
	}
	var index *IndexMetadata
	for i := range m.Indexes {
		if m.Indexes[i].Name == name {
			index = &m.Indexes[i]
			break
		}
	}
	if index == nil || index.Clustered || index.Hidden || index.Algorithm != 2 || (index.Type != 2 && index.Type != 3) {
		return nil, fmt.Errorf("%w: ordinary secondary index %q required", ErrUnsupported, name)
	}
	table := *m.MaterializedSchema
	s := &SecondarySchema{Name: name, Unique: index.Type == 2, RowFormat: table.RowFormat, SpaceID: index.SpaceID, IndexID: index.ID, RootPage: index.RootPage}
	// SDI deduplicates columns by identity; the engine also appends a full
	// clustered field when SDI only contains its prefix. Build both lists.
	expected := []int{}
	expectedOrder := []uint32{}
	for _, e := range index.Elements {
		if e.Hidden {
			continue
		}
		if len(expected) != len(s.Fields) {
			return nil, fmt.Errorf("%w: secondary field order", ErrCorrupt)
		}
		f := SecondaryField{Column: -1}
		source := m.Columns[e.Column]
		if source.Virtual {
			return nil, fmt.Errorf("%w: indexed VIRTUAL column", ErrUnsupported)
		}
		for i, c := range table.Columns {
			if c.Name == source.Name {
				f.Column = i
				f.Definition = c
				break
			}
		}
		if f.Column < 0 || (e.Order != 2 && e.Order != 3) {
			return nil, fmt.Errorf("%w: secondary source/order", ErrUnsupported)
		}
		c := &f.Definition
		c.Descending = e.Order == 3
		if c.isText() {
			c.Collation = map[uint64]string{46: "utf8mb4_bin", 83: "utf8mb3_bin", 65: "ascii_bin", 47: "latin1_bin"}[source.CollationID]
		}
		width := c.keyWidth()

		if width == 0 || e.Length == 0 || e.Length > uint64(width) {
			return nil, fmt.Errorf("%w: secondary field width", ErrUnsupported)
		}
		if e.Length < uint64(width) {
			f.PrefixBytes = int(e.Length)
		}
		s.Fields = append(s.Fields, f)
		expected = append(expected, e.Column)
		expectedOrder = append(expectedOrder, e.Order)
	}
	s.UserFields = len(s.Fields)
	pk, err := table.validate()
	if err != nil {
		return nil, err
	}
	cluster := []SecondaryField{}
	if table.hiddenRowID() {
		cluster = append(cluster, SecondaryField{Column: -1, RowID: true, Definition: Column{Name: "DB_ROW_ID", Type: "BINARY", MaxBytes: 6}})
	} else {
		for _, i := range pk {
			cluster = append(cluster, SecondaryField{Column: i, Definition: table.Columns[i]})
		}
	}
	for _, f := range cluster {
		found := -1
		for i, field := range s.Fields {
			if field.Column == f.Column && field.RowID == f.RowID && field.PrefixBytes == 0 {
				found = i
				break
			}
		}
		if found < 0 {
			found = len(s.Fields)
			s.Fields = append(s.Fields, f)
			pos := -1
			for i, c := range m.Columns {
				if c.Name == f.Definition.Name {
					pos = i
					break
				}
			}
			if pos < 0 {
				return nil, fmt.Errorf("%w: clustered column absent", ErrCorrupt)
			}
			already := false
			for _, old := range expected {
				if old == pos {
					already = true
				}
			}
			if !already {
				expected = append(expected, pos)
				order := uint32(2)
				if f.Definition.Descending {
					order = 3
				}
				expectedOrder = append(expectedOrder, order)
			}
		}
		s.ClusteredFields = append(s.ClusteredFields, found)
	}
	if len(index.Elements) != len(expected) {
		return nil, fmt.Errorf("%w: secondary suffix field count", ErrCorrupt)
	}
	for i, e := range index.Elements {
		if e.Column != expected[i] || e.Hidden != (i >= s.UserFields) {
			return nil, fmt.Errorf("%w: secondary physical suffix", ErrCorrupt)
		}
		order := expectedOrder[i]
		if i >= s.UserFields && (e.Length != uint64(^uint32(0)) || e.Order != order) {
			return nil, fmt.Errorf("%w: secondary hidden SDI element", ErrUnsupported)
		}
	}
	if _, err = s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (f SecondaryField) storageColumn() (Column, error) {
	c := f.Definition
	if f.RowID {
		return Column{Name: "DB_ROW_ID", Type: "BINARY", MaxBytes: 6}, nil
	}
	if f.PrefixBytes < 0 {
		return c, fmt.Errorf("%w: negative prefix", ErrUnsupported)
	}
	if f.PrefixBytes > 0 {
		switch c.Type {
		case "CHAR", "VARCHAR":
			width := c.charsetWidth()
			if width == 0 || f.PrefixBytes%width != 0 || uint64(f.PrefixBytes) > c.variableMaxBytes() {
				return c, fmt.Errorf("%w: character prefix width", ErrUnsupported)
			}
			if c.Type != "CHAR" {
				c.Type = "VARCHAR"
			}
			c.MaxChars = f.PrefixBytes / width
		case "BINARY", "VARBINARY":
			max := c.variableMaxBytes()
			if c.Type == "BINARY" {
				max = uint64(c.MaxBytes)
			}
			if uint64(f.PrefixBytes) > max {
				return c, fmt.Errorf("%w: binary prefix width", ErrUnsupported)
			}
			if c.Type != "BINARY" {
				c.Type = "VARBINARY"
			}
			c.MaxBytes = f.PrefixBytes
		default:
			return c, fmt.Errorf("%w: prefix on fixed scalar", ErrUnsupported)
		}
	}
	return c, nil
}

func (s SecondarySchema) validate() ([]Column, error) {
	bad := func() ([]Column, error) { return nil, fmt.Errorf("%w: secondary schema", ErrUnsupported) }
	if s.Name == "" || s.RootPage == 0 || s.SpaceID == 0 || s.IndexID == 0 || (s.RowFormat != "" && s.RowFormat != "DYNAMIC" && s.RowFormat != "COMPACT") || s.UserFields < 1 || s.UserFields > 16 || len(s.Fields) < s.UserFields || len(s.Fields) > 32 || len(s.ClusteredFields) < 1 || len(s.ClusteredFields) > 16 {
		return bad()
	}
	columns := make([]Column, len(s.Fields))
	total := 0
	for i, f := range s.Fields {
		if f.Definition.Name == "" || f.Column > 1016 || f.Column < 0 && !f.RowID || f.RowID && (f.Column != -1 || f.Definition.Name != "DB_ROW_ID" || f.Definition.Type != "BINARY" || f.Definition.MaxBytes != 6 || f.PrefixBytes != 0 || f.Definition.Nullable || f.Definition.Descending || i < s.UserFields) {
			return bad()
		}
		original := f.Definition
		original.Name = "source"
		original.Collation = ""
		original.Descending = false
		originalSchema := Schema{Columns: []Column{{Name: "key", Type: "INT"}, original}, PrimaryKey: "key", RootPage: s.RootPage, SpaceID: s.SpaceID, IndexID: s.IndexID}
		if _, err := originalSchema.validate(); err != nil {
			return nil, err
		}
		c, err := f.storageColumn()
		if err != nil {
			return nil, err
		}
		if c.keyWidth() == 0 {
			return bad()
		}
		// Reuse the public type validation without making nullable secondary columns
		// pretend to be clustered keys in the actual decoding schema.
		check := c
		check.Name = "field"
		check.Nullable = false
		v := Schema{Columns: []Column{check}, PrimaryKey: "field", RootPage: s.RootPage, SpaceID: s.SpaceID, IndexID: s.IndexID, RowFormat: s.RowFormat}
		if _, err = v.validate(); err != nil {
			return nil, err
		}
		total += c.keyWidth()
		if i == s.UserFields-1 && total > 3072 {
			return bad()
		}
		columns[i] = c
	}
	if total > 6144 {
		return bad()
	}
	used := map[int]bool{}
	rowID := false
	clusterWidth := 0
	for _, i := range s.ClusteredFields {
		if i < 0 || i >= len(s.Fields) || used[i] || s.Fields[i].PrefixBytes != 0 || s.Fields[i].Definition.Nullable {
			return bad()
		}
		used[i] = true
		clusterWidth += columns[i].keyWidth()
		rowID = rowID || s.Fields[i].RowID
	}
	if clusterWidth > 3072 || rowID && len(s.ClusteredFields) != 1 {
		return bad()
	}
	for i := s.UserFields; i < len(s.Fields); i++ {
		if !used[i] {
			return bad()
		}
	}
	return columns, nil
}
