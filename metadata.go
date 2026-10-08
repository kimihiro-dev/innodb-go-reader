package innodb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// TableMetadata is a discovery report, not a promise that every table can be read.
// Schema is nil whenever Issues is nonempty. MaterializedSchema can remain available
// when the only issues are unmaterialized user VIRTUAL columns. Root checks do not scan index rows.
type TableMetadata struct {
	Database, Name     string
	TableID            uint64
	Source             SDIKey
	Columns            []ColumnMetadata
	Indexes            []IndexMetadata
	MaterializedSchema *Schema // Available only when all stored fields/layouts are supported.
	Schema             *Schema
	Issues             []string
}
type ColumnMetadata struct {
	Virtual, Invisible    bool
	GenerationExpression  string
	Name                  string
	DDType                uint32
	CollationID           uint64
	Charset               string
	Hidden                uint32
	Nullable, Unsigned    bool
	CharLength            uint64
	Precision, Scale, FSP int
}
type IndexMetadata struct {
	Clustered         bool // First SDI index; Issues reports unsupported or inconsistent layouts.
	Hidden            bool // SDI hidden index, including generated ROW_ID clustering.
	Name              string
	Type, Algorithm   uint32
	ID                uint64
	SpaceID, RootPage uint32
	Level             uint16
	RootVerified      bool
	Elements          []IndexElement
}
type IndexElement struct {
	Ordinal int    `json:"ordinal_position"`
	Column  int    `json:"column_opx"` // zero-based position in the SDI columns array
	Length  uint64 `json:"length"`
	Order   uint32 `json:"order"`
	Hidden  bool   `json:"hidden"`
}

// InspectTable discovers exactly one table and its tablespace from SDI.
// Unsupported features are reported in Issues; malformed metadata is an error.
func InspectTable(r io.ReaderAt, size int64) (*TableMetadata, error) {
	sdi, err := ReadSDI(r, size)
	if err != nil {
		return nil, err
	}
	return inspectSDITable(r, size, sdi)
}

// ReadAuto reads a supported table without a separately supplied schema.
// A stable supported snapshot remains required; SDI cannot prove transaction history.
func ReadAuto(r io.ReaderAt, size int64) (*Result, error) {
	m, err := InspectTable(r, size)
	if err != nil {
		return nil, err
	}
	if m.Schema == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
	}
	return Read(r, size, *m.Schema)
}

// metadataObject rejects duplicate keys at each metadata object we interpret.
type metadataObject map[string]json.RawMessage

func (o *metadataObject) UnmarshalJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return fmt.Errorf("expected metadata object")
	}
	m := metadataObject{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		key := tok.(string)
		if _, ok := m[key]; ok {
			return fmt.Errorf("duplicate metadata key %q", key)
		}
		var v json.RawMessage
		if err = d.Decode(&v); err != nil {
			return err
		}
		m[key] = v
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	*o = m
	return nil
}
func decodeMetadata(b []byte, v any, required string) error {
	var o metadataObject
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("%w: metadata: %v", ErrCorrupt, err)
	}
	selected := make(map[string]json.RawMessage)
	for _, k := range strings.Fields(required) {
		value, ok := o[k]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%w: missing metadata field %q", ErrCorrupt, k)
		}
		selected[k] = value
	}
	// Do not let encoding/json case-insensitive matching override canonical DD keys.
	b, err := json.Marshal(selected)
	if err != nil {
		return fmt.Errorf("%w: metadata fields: %v", ErrCorrupt, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: metadata field type: %v", ErrCorrupt, err)
	}
	return nil
}

type ddEnvelope struct {
	MySQL   uint32          `json:"mysqld_version_id"`
	DD      uint32          `json:"dd_version"`
	Version uint32          `json:"sdi_version"`
	Kind    string          `json:"dd_object_type"`
	Object  json.RawMessage `json:"dd_object"`
}
type ddTable struct {
	Name         string            `json:"name"`
	Database     string            `json:"schema_ref"`
	ID           uint64            `json:"se_private_id"`
	Engine       string            `json:"engine"`
	RowFormat    uint32            `json:"row_format"`
	Hidden       uint32            `json:"hidden"`
	Partition    uint32            `json:"partition_type"`
	Subpartition uint32            `json:"subpartition_type"`
	Partitions   []json.RawMessage `json:"partitions"`
	Private      string            `json:"se_private_data"`
	Options      string            `json:"options"`
	Columns      []json.RawMessage `json:"columns"`
	Indexes      []json.RawMessage `json:"indexes"`
}
type ddSpace struct {
	Name    string            `json:"name"`
	Engine  string            `json:"engine"`
	Private string            `json:"se_private_data"`
	Options string            `json:"options"`
	Files   []json.RawMessage `json:"files"`
}
type ddColumn struct {
	SRID       *uint32
	Name       string            `json:"name"`
	Type       uint32            `json:"type"`
	Nullable   bool              `json:"is_nullable"`
	Unsigned   bool              `json:"is_unsigned"`
	Virtual    bool              `json:"is_virtual"`
	Hidden     uint32            `json:"hidden"`
	Ordinal    int               `json:"ordinal_position"`
	Length     uint64            `json:"char_length"`
	Precision  int               `json:"numeric_precision"`
	Scale      int               `json:"numeric_scale"`
	FSP        int               `json:"datetime_precision"`
	Expression string            `json:"generation_expression"`
	Private    string            `json:"se_private_data"`
	Options    string            `json:"options"`
	Collation  uint64            `json:"collation_id"`
	Elements   []json.RawMessage `json:"elements"`
}
type ddIndex struct {
	Name      string            `json:"name"`
	Type      uint32            `json:"type"`
	Algorithm uint32            `json:"algorithm"`
	Ordinal   int               `json:"ordinal_position"`
	Hidden    bool              `json:"hidden"`
	Generated bool              `json:"is_generated"`
	Engine    string            `json:"engine"`
	Private   string            `json:"se_private_data"`
	Space     string            `json:"tablespace_ref"`
	Elements  []json.RawMessage `json:"elements"`
}

// Properties used here are MySQL's escaped key=value; serialization, not SQL.
func metadataProperties(s string) (map[string]string, error) {
	out := map[string]string{}
	var key, value strings.Builder
	inValue, escaped := false, false
	for _, c := range s {
		target := &key
		if inValue {
			target = &value
		}
		if escaped {
			if c != '\\' && c != ';' && c != '=' {
				return nil, fmt.Errorf("%w: property escape", ErrCorrupt)
			}
			target.WriteRune(c)
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '=':
			if inValue {
				return nil, fmt.Errorf("%w: unescaped property equals", ErrCorrupt)
			}
			inValue = true
		case ';':
			if !inValue || key.Len() == 0 {
				return nil, fmt.Errorf("%w: malformed property", ErrCorrupt)
			}
			k := key.String()
			if _, ok := out[k]; ok {
				return nil, fmt.Errorf("%w: duplicate property %q", ErrCorrupt, k)
			}
			out[k] = value.String()
			key.Reset()
			value.Reset()
			inValue = false
		default:
			target.WriteRune(c)
		}
	}
	if escaped || inValue || key.Len() != 0 {
		return nil, fmt.Errorf("%w: unterminated property", ErrCorrupt)
	}
	return out, nil
}
func propertyUint(p map[string]string, k string, bits int) (uint64, error) {
	s, ok := p[k]
	if !ok || s == "" {
		return 0, fmt.Errorf("%w: missing property %q", ErrCorrupt, k)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: nondecimal property %q", ErrCorrupt, k)
		}
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%w: property %q out of range", ErrCorrupt, k)
	}
	return n, nil
}

func inspectSDITable(r io.ReaderAt, size int64, sdi *SDIResult) (*TableMetadata, error) {
	return inspectSDITableWithColumnOwner(r, size, sdi, 0)
}

// Partitions share columns owned by the first physical partition. Zero retains
// the ordinary single-table identity check. The collection validator supplies
// a nonzero owner only after validating the complete partition directory.
func inspectSDITableWithColumnOwner(r io.ReaderAt, size int64, sdi *SDIResult, columnOwner uint64) (*TableMetadata, error) {
	m := &TableMetadata{Issues: make([]string, 0)}
	issue := func(s string) { m.Issues = append(m.Issues, s) }
	var tableRaw, spaceRaw json.RawMessage
	for _, rec := range sdi.Records {
		var e ddEnvelope
		if err := decodeMetadata(rec.JSON, &e, "mysqld_version_id dd_version sdi_version dd_object_type dd_object"); err != nil {
			return nil, err
		}
		if e.MySQL != 80045 || e.DD != 80023 || e.Version != 80019 {
			issue("SDI content version is outside 80045/80023/80019")
		}
		switch e.Kind {
		case "Table":
			if tableRaw != nil || rec.Key.Type != 1 {
				return nil, fmt.Errorf("%w: ambiguous SDI table objects", ErrUnsupported)
			}
			tableRaw = e.Object
			m.Source = rec.Key
		case "Tablespace":
			if spaceRaw != nil || rec.Key.Type != 2 {
				return nil, fmt.Errorf("%w: ambiguous SDI tablespace objects", ErrUnsupported)
			}
			spaceRaw = e.Object
		default:
			issue("unrecognized SDI object kind " + e.Kind)
		}
	}
	if tableRaw == nil || spaceRaw == nil {
		return nil, fmt.Errorf("%w: one Table and one Tablespace SDI object required", ErrUnsupported)
	}
	var table ddTable
	var space ddSpace
	if err := decodeMetadata(tableRaw, &table, "name schema_ref se_private_id engine row_format hidden partition_type subpartition_type partitions se_private_data options columns indexes"); err != nil {
		return nil, err
	}
	if err := decodeMetadata(spaceRaw, &space, "name engine se_private_data options files"); err != nil {
		return nil, err
	}
	m.Name, m.Database, m.TableID = table.Name, table.Database, table.ID
	if columnOwner == 0 {
		columnOwner = table.ID
	}
	if table.Name == "" || table.Database == "" || table.ID == 0 {
		return nil, fmt.Errorf("%w: missing table identity", ErrCorrupt)
	}
	if table.Engine != "InnoDB" || space.Engine != "InnoDB" {
		issue("engine is not InnoDB")
	}
	if (table.RowFormat != 2 && table.RowFormat != 5) || table.Hidden != 1 {
		issue("table row format/visibility is unsupported")
	}
	if table.Partition != 0 || table.Subpartition != 0 || len(table.Partitions) != 0 {
		issue("partitioned table is unsupported")
	}
	tp, err := metadataProperties(table.Private)
	if err != nil {
		return nil, err
	}
	for k := range tp {
		if k != "autoinc" {
			issue("table private attribute requires unsupported layout: " + k)
		}
	}
	opt, err := metadataProperties(table.Options)
	if err != nil {
		return nil, err
	}
	if opt["encrypt_type"] != "N" {
		issue("table encryption option is not N")
	}
	sp, err := metadataProperties(space.Private)
	if err != nil {
		return nil, err
	}
	sid, err := propertyUint(sp, "id", 32)
	if err != nil {
		return nil, err
	}
	if uint32(sid) != sdi.SpaceID {
		return nil, fmt.Errorf("%w: SDI tablespace ID mismatch", ErrCorrupt)
	}
	flags, err := propertyUint(sp, "flags", 32)
	if err != nil {
		return nil, err
	}
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return nil, err
	}
	if uint32(flags) != be.Uint32(fsp[54:58]) {
		return nil, fmt.Errorf("%w: SDI/FSP flags mismatch", ErrCorrupt)
	}
	sop, err := metadataProperties(space.Options)
	if err != nil {
		return nil, err
	}
	if sop["encryption"] != "N" || sp["state"] != "normal" || len(space.Files) != 1 {
		issue("tablespace encryption/state/file count unsupported")
	}
	if len(space.Files) == 1 {
		var file struct {
			Private string `json:"se_private_data"`
		}
		if err := decodeMetadata(space.Files[0], &file, "se_private_data"); err != nil {
			return nil, err
		}
		fp, err := metadataProperties(file.Private)
		if err != nil {
			return nil, err
		}
		id, err := propertyUint(fp, "id", 32)
		if err != nil {
			return nil, err
		}
		if id != sid {
			return nil, fmt.Errorf("%w: tablespace file ID mismatch", ErrCorrupt)
		}
	}
	if len(table.Columns) < 1 || len(table.Columns) > 1020 {
		return nil, fmt.Errorf("%w: DD column count", ErrUnsupported)
	}
	columns := make([]ddColumn, len(table.Columns))
	schema := Schema{SpaceID: sdi.SpaceID}
	if table.RowFormat == 5 {
		schema.RowFormat = "COMPACT"
	}
	if err := checkTablespace(fsp, schema); err != nil {
		issue(err.Error())
	}
	userPositions := []int{}
	systemPositions := []int{}
	rowIDPosition := -1
	names := map[string]bool{}
	for i, raw := range table.Columns {
		c := &columns[i]
		if err := decodeMetadata(raw, c, "name type is_nullable is_unsigned is_virtual hidden ordinal_position char_length numeric_precision numeric_scale datetime_precision generation_expression se_private_data options collation_id elements"); err != nil {
			return nil, err
		}
		if c.Type == 30 {
			var spatial struct {
				Null bool   `json:"srs_id_null"`
				ID   uint32 `json:"srs_id"`
			}
			if err := decodeMetadata(raw, &spatial, "srs_id_null srs_id"); err != nil {
				return nil, err
			}
			if !spatial.Null {
				c.SRID = &spatial.ID
			}
		}
		if c.Ordinal != i+1 || c.Name == "" || names[c.Name] {
			return nil, fmt.Errorf("%w: DD column ordinal/name", ErrCorrupt)
		}
		names[c.Name] = true
		props, err := metadataProperties(c.Private)
		if err != nil {
			return nil, err
		}
		dropped := props["version_dropped"] != "" && c.Hidden == 2
		if _, exists := props["table_id"]; exists || !dropped {
			id, err := propertyUint(props, "table_id", 64)
			if err != nil {
				return nil, err
			}
			if id != columnOwner {
				return nil, fmt.Errorf("%w: column table ID", ErrCorrupt)
			}
		}
		for k := range props {
			switch k {
			case "table_id", "physical_pos", "version_added", "version_dropped", "default", "default_null":
			default:
				issue(fmt.Sprintf("column %s has unsupported private attribute %s", c.Name, k))
			}
		}
		charset := "unknown"
		switch c.Collation {
		case 255:
			charset = "utf8mb4"
		case 63:
			charset = "binary"
		case 8:
			charset = "latin1"
		}
		m.Columns = append(m.Columns, ColumnMetadata{Name: c.Name, DDType: c.Type, CollationID: c.Collation, Charset: charset, Hidden: c.Hidden, Nullable: c.Nullable, Unsigned: c.Unsigned, CharLength: c.Length, Precision: c.Precision, Scale: c.Scale, FSP: c.FSP, Virtual: c.Virtual, Invisible: c.Hidden == 4, GenerationExpression: c.Expression})
		if c.Hidden == 2 && (c.Name == "DB_ROW_ID" || c.Name == "DB_TRX_ID" || c.Name == "DB_ROLL_PTR") {
			if c.Name == "DB_ROW_ID" {
				rowIDPosition = i
			} else {
				systemPositions = append(systemPositions, i)
			}
			typ, length := uint32(10), uint64(6)
			if c.Name == "DB_ROLL_PTR" {
				typ = 9
				length = 7
			}
			if c.Type != typ || c.Length != length || c.Nullable || c.Virtual || c.Expression != "" || c.Collation != 63 || (c.Name == "DB_ROW_ID" && c.Unsigned) {
				return nil, fmt.Errorf("%w: system column layout", ErrCorrupt)
			}
			continue
		}
		if dropped {
			if c.Virtual || c.Expression != "" {
				issue("dropped generated column unsupported")
			}
			continue
		}
		if c.Hidden != 1 && c.Hidden != 4 {
			issue("column " + c.Name + " has unsupported internal visibility")
		}
		if c.Virtual {
			if c.Expression == "" {
				return nil, fmt.Errorf("%w: VIRTUAL column lacks generation expression", ErrCorrupt)
			}
			if len(props) != 1 {
				issue("VIRTUAL column " + c.Name + " has physical layout attributes")
			}
			schema.VirtualColumns = append(schema.VirtualColumns, VirtualColumn{Name: c.Name, Ordinal: len(schema.Columns) + len(schema.VirtualColumns) + 1, Expression: c.Expression, Invisible: c.Hidden == 4})
			continue
		}
		userPositions = append(userPositions, i)
		converted, reason, err := metadataColumn(*c)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			issue("column " + c.Name + ": " + reason)
		}
		schema.Columns = append(schema.Columns, converted)
	}
	if len(systemPositions) != 2 || columns[systemPositions[0]].Name != "DB_TRX_ID" || columns[systemPositions[1]].Name != "DB_ROLL_PTR" {
		issue("required DB_TRX_ID/DB_ROLL_PTR columns are absent or reordered")
	}
	primary := -1
	indexIDs := map[uint64]bool{}
	roots := map[uint32]bool{}
	indexNames := map[string]bool{}
	for i, raw := range table.Indexes {
		var idx ddIndex
		if err := decodeMetadata(raw, &idx, "name type algorithm ordinal_position hidden is_generated engine se_private_data tablespace_ref elements"); err != nil {
			return nil, err
		}
		if idx.Ordinal != i+1 || idx.Name == "" || indexNames[idx.Name] {
			return nil, fmt.Errorf("%w: index ordinal/name", ErrCorrupt)
		}
		indexNames[idx.Name] = true
		entry := IndexMetadata{Name: idx.Name, Type: idx.Type, Algorithm: idx.Algorithm, Clustered: i == 0, Hidden: idx.Hidden}
		for j, raw := range idx.Elements {
			var e IndexElement
			if err := decodeMetadata(raw, &e, "ordinal_position length order hidden column_opx"); err != nil {
				return nil, err
			}
			if e.Ordinal != j+1 || e.Column < 0 || e.Column >= len(columns) {
				return nil, fmt.Errorf("%w: index element position", ErrCorrupt)
			}
			entry.Elements = append(entry.Elements, e)
		}
		if idx.Type == 1 {
			if primary != -1 {
				return nil, fmt.Errorf("%w: multiple primary indexes", ErrCorrupt)
			}
			primary = i
		}
		if idx.Algorithm != 2 || idx.Engine != "InnoDB" || (idx.Type < 1 || idx.Type > 3) || (idx.Hidden && !(i == 0 && idx.Type == 2 && idx.Name == "PRIMARY" && rowIDPosition >= 0)) || idx.Generated {
			issue("index " + idx.Name + " kind/algorithm/visibility unsupported")
		}
		props, err := metadataProperties(idx.Private)
		if err != nil {
			return nil, err
		}
		if len(props) == 0 && len(table.Partitions) > 0 {
			m.Indexes = append(m.Indexes, entry)
			continue
		}
		id, err := propertyUint(props, "id", 64)
		if err != nil {
			return nil, err
		}
		root, err := propertyUint(props, "root", 32)
		if err != nil {
			return nil, err
		}
		spaceID, err := propertyUint(props, "space_id", 32)
		if err != nil {
			return nil, err
		}
		tableID, err := propertyUint(props, "table_id", 64)
		if err != nil {
			return nil, err
		}
		if id == 0 || root == 0 || tableID != table.ID || spaceID != sid || idx.Space != space.Name || indexIDs[id] || roots[uint32(root)] {
			return nil, fmt.Errorf("%w: index identity/root/tablespace mismatch", ErrCorrupt)
		}
		indexIDs[id] = true
		roots[uint32(root)] = true
		entry.ID = id
		entry.RootPage = uint32(root)
		entry.SpaceID = uint32(spaceID)
		if idx.Algorithm == 2 {
			page, err := readPage(r, size, entry.RootPage)
			if err != nil {
				return nil, err
			}
			minimum := 10
			if !entry.Clustered {
				minimum = 6
			}
			parsed, err := parseIndexMinimum(page, Schema{SpaceID: entry.SpaceID, IndexID: entry.ID}, minimum)
			if err != nil {
				return nil, err
			}
			if parsed.Previous != ^uint32(0) || parsed.Next != ^uint32(0) {
				return nil, fmt.Errorf("%w: index root has siblings", ErrCorrupt)
			}
			entry.Level = parsed.Level
			entry.RootVerified = true
		}
		m.Indexes = append(m.Indexes, entry)
	}
	if primary > 0 {
		issue("explicit primary index must be the first clustered index")
	}
	if len(m.Indexes) == 0 {
		issue("clustered index absent")
	} else {
		primary = 0
		idx := m.Indexes[primary]
		if idx.Type != 1 && idx.Type != 2 {
			issue("first index is not a clustered candidate")
		}
		if rowIDPosition >= 0 {
			if idx.Type != 2 || !idx.Hidden || idx.Name != "PRIMARY" {
				issue("ROW_ID does not match hidden clustered index")
			}
			expected := append([]int{rowIDPosition}, systemPositions...)
			expected = append(expected, userPositions...)
			if len(idx.Elements) != len(expected) {
				issue("hidden clustered field count")
			} else {
				for j, pos := range expected {
					e := idx.Elements[j]
					if e.Column != pos || !e.Hidden || e.Order != 2 || e.Length != uint64(^uint32(0)) {
						issue("hidden clustered field order/length")
						break
					}
				}
			}
			schema.ClusteredKey = &ClusteredKey{Name: idx.Name, HiddenRowID: true}
			schema.RootPage = idx.RootPage
			schema.IndexID = idx.ID
		} else {
			if idx.Hidden {
				issue("hidden clustered index lacks ROW_ID")
			}

			keys := []IndexElement{}
			for _, e := range idx.Elements {
				if !e.Hidden {
					keys = append(keys, e)
				}
			}
			if len(keys) == 0 || len(keys) > 16 {
				issue("primary key requires 1..16 columns")
			} else {
				expected := []int{}
				keyNames := []string{}
				seen := map[int]bool{}
				for _, key := range keys {
					col := columns[key.Column]
					converted, _, err := metadataColumn(col)
					if err != nil {
						return nil, err
					}
					if converted.Type == "CHAR" || converted.Type == "VARCHAR" {
						converted.Collation = map[uint64]string{46: "utf8mb4_bin", 83: "utf8mb3_bin", 65: "ascii_bin", 47: "latin1_bin"}[uint64(col.Collation)]
					}
					converted.Descending = key.Order == 3
					width := converted.keyWidth()
					for j, pos := range userPositions {
						if pos == key.Column {
							schema.Columns[j] = converted
						}
					}
					if seen[key.Column] || (col.Hidden != 1 && col.Hidden != 4) || col.Virtual || col.Nullable || width == 0 || (key.Order != 2 && key.Order != 3) || key.Length != uint64(width) {
						issue("primary key requires distinct full non-null supported types and ordering")
					}
					seen[key.Column] = true
					expected = append(expected, key.Column)
					keyNames = append(keyNames, col.Name)
				}
				if idx.Type == 2 {
					schema.ClusteredKey = &ClusteredKey{Name: idx.Name, Columns: keyNames}
				} else if len(keyNames) == 1 {
					schema.PrimaryKey = keyNames[0]
				} else {
					schema.PrimaryKeys = keyNames
				}
				schema.RootPage = idx.RootPage
				schema.IndexID = idx.ID
				expected = append(expected, systemPositions...)
				for _, pos := range userPositions {
					if !seen[pos] {
						expected = append(expected, pos)
					}
				}
				if len(expected) != len(idx.Elements) {
					issue("clustered physical field count unsupported")
				} else {
					for j, pos := range expected {
						e := idx.Elements[j]
						hidden := j >= len(keys)
						if e.Column != pos || (hidden && e.Order != 2) || e.Hidden != hidden || hidden && e.Length != uint64(^uint32(0)) {
							issue("clustered physical field order/length unsupported")
							break
						}
					}
				}
			}
		}

	}

	if len(m.Issues) == 0 {
		reason, err := metadataInstant(&schema, columns)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			issue(reason)
		}
	}
	if len(m.Issues) == 0 {
		if _, err := schema.validate(); err != nil {
			issue(err.Error())
		} else {
			m.MaterializedSchema = &schema
			if len(schema.VirtualColumns) == 0 {
				m.Schema = &schema
			}
		}
	}
	for _, c := range schema.VirtualColumns {
		issue("column " + c.Name + " is VIRTUAL and unmaterialized; use ReadMaterializedAuto explicitly")
	}
	return m, nil
}

func ddIntegerType(t uint32) string {
	switch t {
	case 2:
		return "TINYINT"
	case 3:
		return "SMALLINT"
	case 4:
		return "INT"
	case 9:
		return "BIGINT"
	case 10:
		return "MEDIUMINT"
	}
	return ""
}
func metadataColumn(c ddColumn) (Column, string, error) {
	out := Column{Name: c.Name, Nullable: c.Nullable, Invisible: c.Hidden == 4, GenerationExpression: c.Expression}
	// DD also uses unsigned on temporal fields; our attribute applies only to numeric types.
	if ddIntegerType(c.Type) != "" || c.Type == 5 || c.Type == 6 || c.Type == 21 {
		out.Unsigned = c.Unsigned
	}
	out.Type = ddIntegerType(c.Type)
	if out.Type != "" {
		return out, "", nil
	}
	switch c.Type {
	case 30:
		if c.Collation != 63 || c.Length != 4294967295 {
			return out, "geometry binary collation/capacity unsupported", nil
		}
		props, err := metadataProperties(c.Options)
		if err != nil {
			return out, "", err
		}
		typ, err := propertyUint(props, "geom_type", 32)
		if err != nil {
			return out, "", err
		}
		if typ > 7 {
			return out, "geometry subtype unsupported", nil
		}
		out.Type = geometryTypes[typ]
		out.SRID = c.SRID
	case 31:
		out.Type = "JSON"
		if c.Collation != 63 || c.Length != 4294967295 {
			return out, "JSON binary collation/capacity unsupported", nil
		}
	case 5:
		out.Type = "FLOAT"
	case 6:
		out.Type = "DOUBLE"
	case 14:
		out.Type = "YEAR"
	case 15:
		out.Type = "DATE"
	case 18:
		out.Type = "TIMESTAMP"
		out.FSP = c.FSP
	case 19:
		out.Type = "DATETIME"
		out.FSP = c.FSP
	case 20:
		out.Type = "TIME"
		out.FSP = c.FSP
	case 21:
		out.Type = "DECIMAL"
		out.Precision = c.Precision
		out.Scale = c.Scale
	case 17:
		out.Type = "BIT"
		if c.Length > 64 {
			return out, "BIT width unsupported", nil
		}
		out.BitLength = int(c.Length)
	case 16, 29:
		if c.Collation == 63 {
			out.Type = "VARBINARY"
			if c.Type == 29 {
				out.Type = "BINARY"
			}
			if c.Length > 65535 {
				return out, "binary byte width unsupported", nil
			}
			out.MaxBytes = int(c.Length)
		} else {
			out.Type = "VARCHAR"
			if c.Type == 29 {
				out.Type = "CHAR"
			}
			charset, ok := metadataCharset(c.Collation)
			if !ok {
				return out, "text collation outside verified charset mapping", nil
			}
			out.Charset = charset
			width := uint64(out.charsetWidth())
			if c.Length%width != 0 || c.Length > 65535 {
				return out, "invalid text byte length", nil
			}
			out.MaxChars = int(c.Length / width)
		}
	case 22, 23:
		charset, ok := metadataCharset(c.Collation)
		if !ok {
			return out, "dictionary charset/collation unsupported", nil
		}
		out.Charset = charset
		if len(c.Elements) > 65535 {
			return out, "dictionary count unsupported", nil
		}
		labels := []string{}
		for i, raw := range c.Elements {
			var item struct {
				Name  string `json:"name"`
				Index int    `json:"index"`
			}
			if err := decodeMetadata(raw, &item, "name index"); err != nil {
				return out, "", err
			}
			if item.Index != i+1 {
				return out, "", fmt.Errorf("%w: dictionary ordinal", ErrCorrupt)
			}
			label, err := base64.StdEncoding.Strict().DecodeString(item.Name)
			if err != nil {
				return out, "", fmt.Errorf("%w: dictionary Base64", ErrCorrupt)
			}
			text, err := decodeText(out.charsetName(), label)
			if err != nil {
				return out, "", err
			}
			labels = append(labels, text)
		}
		props, err := metadataProperties(c.Options)
		if err != nil {
			return out, "", err
		}
		count, err := propertyUint(props, "interval_count", 32)
		if err != nil {
			return out, "", err
		}
		if count != uint64(len(labels)) {
			return out, "", fmt.Errorf("%w: dictionary count mismatch", ErrCorrupt)
		}
		out.Type = "ENUM"
		out.EnumValues = labels
		if c.Type == 23 {
			out.Type = "SET"
			out.EnumValues = nil
			out.SetValues = labels
		}
	case 24, 25, 26, 27:
		names := map[uint32]string{24: "TINY", 25: "MEDIUM", 26: "LONG", 27: ""}
		suffix := "TEXT"
		if c.Collation == 63 {
			suffix = "BLOB"
		} else {
			charset, ok := metadataCharset(c.Collation)
			if !ok {
				return out, "text collation unsupported", nil
			}
			out.Charset = charset
		}
		out.Type = names[c.Type] + suffix
		if c.Length != out.lobTypeMaxBytes() {
			return out, "LOB declared byte capacity mismatch", nil
		}
	default:
		return out, fmt.Sprintf("DD type %d is unsupported", c.Type), nil
	}
	return out, "", nil
}
