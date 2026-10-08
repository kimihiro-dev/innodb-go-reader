// Package innodb reads restricted InnoDB clustered and secondary index snapshots.
// Read accepts trusted schema; ReadAuto discovers supported metadata from SDI.
package innodb

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupported = errors.New("unsupported InnoDB layout")
	ErrCorrupt     = errors.New("invalid InnoDB data")
)

// Column describes a stored user column in SQL declaration order. Charset defaults to utf8mb4 for text and dictionaries.
type Column struct {
	Invisible            bool     `json:"invisible,omitempty"`             // User INVISIBLE, still stored and returned.
	GenerationExpression string   `json:"generation_expression,omitempty"` // STORED expression; never evaluated.
	Collation            string   `json:"collation,omitempty"`             // Explicit supported _bin rule for character keys.
	Descending           bool     `json:"descending,omitempty"`            // Clustered key member direction.
	Charset              string   `json:"charset,omitempty"`               // utf8mb4 (default), utf8mb3/utf8, ascii, or MySQL latin1
	SRID                 *uint32  `json:"srid,omitempty"`                  // Geometry only: nil is unconstrained; a pointer to zero requires SRID 0.
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`                  // TINYINT, SMALLINT, MEDIUMINT, INT, BIGINT, VARCHAR, VARBINARY, a TEXT/BLOB variant, DECIMAL, FLOAT, DOUBLE, DATE, YEAR, DATETIME, TIME, TIMESTAMP, BIT, BINARY, ENUM, SET, CHAR, JSON, or a geometry type
	SetValues            []string `json:"set_values,omitempty"`  // SET dictionary in stored declaration order
	EnumValues           []string `json:"enum_values,omitempty"` // ENUM dictionary in stored declaration order
	BitLength            int      `json:"bit_length,omitempty"`  // BIT: 1..64
	FSP                  int      `json:"fsp,omitempty"`         // DATETIME/TIME/TIMESTAMP fractional seconds: 0..6
	Precision            int      `json:"precision,omitempty"`   // DECIMAL: 1..65
	Scale                int      `json:"scale,omitempty"`       // DECIMAL: 0..min(precision,30)
	Unsigned             bool     `json:"unsigned,omitempty"`
	Nullable             bool     `json:"nullable"`
	MaxChars             int      `json:"max_chars"`           // VARCHAR: 0..65535/charset width; CHAR: 0..255; otherwise 0
	MaxBytes             int      `json:"max_bytes,omitempty"` // VARBINARY: 0..65535; BINARY fixed width: 0..255; otherwise 0
}

// variableMaxBytes is only used after schema validation.
func (c Column) variableMaxBytes() uint64 {
	if c.Type == "VARCHAR" || c.Type == "CHAR" {
		return uint64(c.MaxChars) * uint64(c.charsetWidth())
	}
	if c.Type == "VARBINARY" {
		return uint64(c.MaxBytes)
	}
	return c.lobTypeMaxBytes()
}

// lobTypeMaxBytes returns SQL byte limits, independent of character width.
func (c Column) lobTypeMaxBytes() uint64 {
	if geometryType(c.Type) {
		return 4294967295
	}
	switch c.Type {
	case "TINYTEXT", "TINYBLOB":
		return 255
	case "TEXT", "BLOB":
		return 65535
	case "MEDIUMTEXT", "MEDIUMBLOB":
		return 16777215
	case "LONGTEXT", "LONGBLOB", "JSON":
		return 4294967295
	}
	return 0
}

func (c Column) isBinary() bool {
	switch c.Type {
	case "BINARY", "VARBINARY", "TINYBLOB", "BLOB", "MEDIUMBLOB", "LONGBLOB":
		return true
	}
	return false
}

// Schema describes the supported physical layout, supplied by the caller or InspectTable.
// It describes a supported stable DYNAMIC/COMPACT clustered layout; SecondarySchema describes secondary layouts.
// ClusteredKey describes a non-primary clustered index. Name is the SDI name.
type ClusteredKey struct {
	Name        string   `json:"name"`
	Columns     []string `json:"columns,omitempty"`
	HiddenRowID bool     `json:"hidden_row_id,omitempty"`
}

// VirtualColumn describes a user column absent from clustered row storage.
// Ordinal is its one-based position among current SQL user columns.
type VirtualColumn struct {
	Name       string `json:"name"`
	Ordinal    int    `json:"ordinal"`
	Expression string `json:"expression"`
	Invisible  bool   `json:"invisible,omitempty"`
}

type Schema struct {
	RowFormat      string          `json:"row_format,omitempty"` // Empty means DYNAMIC; COMPACT uses local LOB prefixes.
	VirtualColumns []VirtualColumn `json:"virtual_columns,omitempty"`
	Instant        *InstantLayout  `json:"instant,omitempty"`
	ClusteredKey   *ClusteredKey   `json:"clustered_key,omitempty"`
	Columns        []Column        `json:"columns"`
	PrimaryKey     string          `json:"primary_key,omitempty"`
	PrimaryKeys    []string        `json:"primary_keys,omitempty"`
	RootPage       uint32          `json:"root_page"`
	SpaceID        uint32          `json:"space_id"`
	IndexID        uint64          `json:"index_id"`
}

func (s Schema) validate() ([]int, error) {
	if s.RowFormat != "" && s.RowFormat != "DYNAMIC" && s.RowFormat != "COMPACT" {
		return nil, fmt.Errorf("%w: row format %q", ErrUnsupported, s.RowFormat)
	}
	if len(s.Columns) == 0 || len(s.Columns) > 1017 || s.RootPage == 0 || s.SpaceID == 0 || s.IndexID == 0 {
		return nil, fmt.Errorf("%w: columns and nonzero root/space/index IDs required", ErrUnsupported)
	}
	names := make(map[string]bool)
	lastOrdinal := 0
	if len(s.Columns)+len(s.VirtualColumns) > 1017 {
		return nil, fmt.Errorf("%w: too many logical columns", ErrUnsupported)
	}
	for _, c := range s.VirtualColumns {
		if c.Name == "" || names[c.Name] || c.Expression == "" || c.Ordinal <= lastOrdinal || c.Ordinal > len(s.Columns)+len(s.VirtualColumns) {
			return nil, fmt.Errorf("%w: virtual column name/expression/ordinal", ErrUnsupported)
		}
		names[c.Name] = true
		lastOrdinal = c.Ordinal
	}
	for _, c := range s.Columns {
		if c.Name == "" || names[c.Name] {
			return nil, fmt.Errorf("%w: empty or duplicate column %q", ErrUnsupported, c.Name)
		}
		names[c.Name] = true
		if c.Type != "DECIMAL" && (c.Precision != 0 || c.Scale != 0) {
			return nil, fmt.Errorf("%w: decimal attributes on column %q", ErrUnsupported, c.Name)
		}
		if c.Type != "DATETIME" && c.Type != "TIME" && c.Type != "TIMESTAMP" && c.FSP != 0 {
			return nil, fmt.Errorf("%w: fractional seconds on column %q", ErrUnsupported, c.Name)
		}
		if c.Type != "BIT" && c.BitLength != 0 {
			return nil, fmt.Errorf("%w: bit length on column %q", ErrUnsupported, c.Name)
		}
		if c.Type != "ENUM" && len(c.EnumValues) != 0 {
			return nil, fmt.Errorf("%w: enum dictionary on column %q", ErrUnsupported, c.Name)
		}
		if c.Type != "SET" && len(c.SetValues) != 0 {
			return nil, fmt.Errorf("%w: set dictionary on column %q", ErrUnsupported, c.Name)
		}
		if c.SRID != nil && !geometryType(c.Type) {
			return nil, fmt.Errorf("%w: SRID on non-geometry column", ErrUnsupported)
		}
		if c.isText() || c.Type == "ENUM" || c.Type == "SET" {
			if c.charsetWidth() == 0 {
				return nil, fmt.Errorf("%w: charset %q", ErrUnsupported, c.Charset)
			}
		} else if c.Charset != "" {
			return nil, fmt.Errorf("%w: charset on non-text column", ErrUnsupported)
		}
		valid := false
		switch c.Type {
		case "SET":
			valid = len(c.SetValues) >= 1 && len(c.SetValues) <= 64 && !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
			for _, label := range c.SetValues {
				if !textRepresentable(c.charsetName(), label) || strings.Contains(label, ",") {
					valid = false
					break
				}
			}
		case "ENUM":
			valid = len(c.EnumValues) >= 1 && len(c.EnumValues) <= 65535 && !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
			for _, label := range c.EnumValues {
				if !textRepresentable(c.charsetName(), label) {
					valid = false
					break
				}
			}
		case "BIT":
			valid = c.BitLength >= 1 && c.BitLength <= 64 && !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
		case "DATETIME", "TIME", "TIMESTAMP":
			valid = c.FSP >= 0 && c.FSP <= 6 && !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
		case "DATE", "YEAR":
			valid = !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
		case "FLOAT", "DOUBLE":
			valid = c.MaxChars == 0 && c.MaxBytes == 0
		case "DECIMAL":
			valid = c.Precision >= 1 && c.Precision <= 65 && c.Scale >= 0 && c.Scale <= 30 && c.Scale <= c.Precision && c.MaxChars == 0 && c.MaxBytes == 0
		case "CHAR":
			valid = !c.Unsigned && c.MaxBytes == 0 && c.MaxChars >= 0 && c.MaxChars <= 255
		case "VARCHAR":
			valid = !c.Unsigned && c.MaxBytes == 0 && c.MaxChars >= 0 && c.MaxChars <= 65535/c.charsetWidth()
		case "BINARY":
			valid = !c.Unsigned && c.MaxChars == 0 && c.MaxBytes >= 0 && c.MaxBytes <= 255
		case "VARBINARY":
			valid = !c.Unsigned && c.MaxChars == 0 && c.MaxBytes >= 0 && c.MaxBytes <= 65535
		case "GEOMETRY", "POINT", "LINESTRING", "POLYGON", "MULTIPOINT", "MULTILINESTRING", "MULTIPOLYGON", "GEOMETRYCOLLECTION", "JSON", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT", "TINYBLOB", "BLOB", "MEDIUMBLOB", "LONGBLOB":
			valid = !c.Unsigned && c.MaxChars == 0 && c.MaxBytes == 0
		default:
			valid = integerWidth(c.Type) != 0 && c.MaxChars == 0 && c.MaxBytes == 0
		}
		if !valid {
			return nil, fmt.Errorf("%w: invalid type/length attributes for column %q", ErrUnsupported, c.Name)
		}
	}
	keys := s.PrimaryKeys
	if s.PrimaryKey != "" {
		if len(keys) != 0 {
			return nil, fmt.Errorf("%w: primary_key and primary_keys are mutually exclusive", ErrUnsupported)
		}
		keys = []string{s.PrimaryKey}
	}
	if s.ClusteredKey != nil {
		c := s.ClusteredKey
		if s.PrimaryKey != "" || len(s.PrimaryKeys) != 0 || c.Name == "" {
			return nil, fmt.Errorf("%w: conflicting/incomplete clustered key", ErrUnsupported)
		}
		if c.HiddenRowID {
			if len(c.Columns) != 0 {
				return nil, fmt.Errorf("%w: hidden key has user columns", ErrUnsupported)
			}
			for _, col := range s.Columns {
				if col.Descending || col.Collation != "" {
					return nil, fmt.Errorf("%w: attributes on non-key column", ErrUnsupported)
				}
			}
			return []int{}, s.validateInstant([]int{})
		}
		keys = c.Columns
	}
	if len(keys) == 0 || len(keys) > 16 {
		return nil, fmt.Errorf("%w: primary key requires 1..16 columns", ErrUnsupported)
	}
	total := 0
	pk := make([]int, 0, len(keys))
	seen := map[string]bool{}
	for _, name := range keys {
		if seen[name] {
			return nil, fmt.Errorf("%w: duplicate primary key column", ErrUnsupported)
		}
		seen[name] = true
		found := -1
		for i, c := range s.Columns {
			if c.Name == name {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("%w: primary key column not found", ErrUnsupported)
		}
		c := s.Columns[found]
		if c.keyWidth() == 0 || c.Nullable {
			return nil, fmt.Errorf("%w: unsupported primary key type, collation, or nullability", ErrUnsupported)
		}
		if s.RowFormat == "COMPACT" && c.keyWidth() > 767 {
			return nil, fmt.Errorf("%w: COMPACT key member exceeds 767 bytes", ErrUnsupported)
		}
		total += c.keyWidth()
		pk = append(pk, found)
	}
	if total > 3072 {
		return nil, fmt.Errorf("%w: primary key exceeds 3072 bytes", ErrUnsupported)
	}
	for _, c := range s.Columns {
		if !seen[c.Name] && (c.Collation != "" || c.Descending) {
			return nil, fmt.Errorf("%w: key attributes on non-key column", ErrUnsupported)
		}
	}
	return pk, s.validateInstant(pk)
}

func (s Schema) hiddenRowID() bool { return s.ClusteredKey != nil && s.ClusteredKey.HiddenRowID }
