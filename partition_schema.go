package innodb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// PartitionSchema is a trusted complete MySQL 5.7 partition directory. Files
// cannot prove the logical table identity or the completeness of this source.
type PartitionSchema struct {
	Version       int                    `json:"version"`
	MySQLVersion  string                 `json:"mysql_version"`
	Database      string                 `json:"database"`
	Table         string                 `json:"table"`
	PartitionType uint32                 `json:"partition_type"`
	Expression    string                 `json:"expression"`
	Partitions    []PartitionSchemaEntry `json:"partitions"`
}

type PartitionSchemaEntry struct {
	Name    string            `json:"name"`
	Number  uint32            `json:"number"`
	TableID uint64            `json:"table_id"`
	Schema  Schema            `json:"schema"`
	Indexes []SecondarySchema `json:"indexes,omitempty"`
}

// ValidatePartitionSchema validates the trusted definition, without reading
// files or authenticating its SQL source. Physical validation is a separate step.
func ValidatePartitionSchema(s PartitionSchema) error {
	bad := func(reason string) error { return fmt.Errorf("%w: partition schema %s", ErrUnsupported, reason) }
	if s.Version != 1 || !utf8.ValidString(s.Database+s.Table+s.Expression) || s.Database == "" || s.Table == "" || strings.ContainsRune(s.Database+s.Table, 0) {
		return bad("version/logical identity")
	}
	switch strings.SplitN(s.MySQLVersion, "-", 2)[0] {
	case "5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43", "5.7.44":
	default:
		return bad("unvalidated MySQL version")
	}
	if s.PartitionType != 1 && s.PartitionType != 3 && s.PartitionType != 7 && s.PartitionType != 8 {
		return bad("method")
	}
	if len(s.Partitions) == 0 || len(s.Partitions) > MaxPartitionFiles {
		return bad("directory size")
	}
	names, tables, spaces, indexes := map[string]bool{}, map[uint64]bool{}, map[uint32]bool{}, map[uint64]bool{}
	var layout Schema
	var secondary []SecondarySchema
	for i, p := range s.Partitions {
		if p.Name == "" || !utf8.ValidString(p.Name) || strings.ContainsRune(p.Name, 0) || names[p.Name] || p.Number != uint32(i) || p.TableID == 0 || p.TableID == ^uint64(0) || tables[p.TableID] || spaces[p.Schema.SpaceID] || indexes[p.Schema.IndexID] {
			return bad("duplicate/invalid partition identity or order")
		}
		names[p.Name], tables[p.TableID], spaces[p.Schema.SpaceID], indexes[p.Schema.IndexID] = true, true, true, true
		if _, err := p.Schema.validate(); err != nil {
			return err
		}
		if p.Schema.Instant != nil || p.Schema.LegacyInstant != nil {
			return bad("5.7 INSTANT layout")
		}
		for _, c := range p.Schema.Columns {
			if c.Descending || c.Invisible {
				return bad("5.7 descending/invisible layout")
			}
		}
		for _, c := range p.Schema.VirtualColumns {
			if c.Invisible {
				return bad("5.7 invisible VIRTUAL layout")
			}
		}
		l := p.Schema
		l.RootPage, l.SpaceID, l.IndexID = 0, 0, 0
		if l.RowFormat == "" {
			l.RowFormat = "DYNAMIC"
		}
		idx := append([]SecondarySchema(nil), p.Indexes...)
		for j, item := range idx {
			for _, f := range item.Fields {
				if f.Definition.Descending || f.Definition.Invisible {
					return bad("5.7 descending/invisible index layout")
				}
			}
			if indexes[item.IndexID] {
				return bad("duplicate index identity")
			}
			indexes[item.IndexID] = true
			if err := validateSecondaryTable(p.Schema, item); err != nil {
				return err
			}
			idx[j].RootPage, idx[j].SpaceID, idx[j].IndexID = 0, 0, 0
			if idx[j].RowFormat == "" {
				idx[j].RowFormat = "DYNAMIC"
			}
		}
		if i == 0 {
			layout, secondary = l, idx
		} else if !reflect.DeepEqual(layout, l) || !reflect.DeepEqual(secondary, idx) {
			return bad("logical columns/key/index mismatch")
		}
	}
	return nil
}

func inspectTrustedPartitionSet(reader *scanReader, inputs []PartitionInput, definition PartitionSchema) (*PartitionSet, []orderedPartition, error) {
	if err := ValidatePartitionSchema(definition); err != nil {
		return nil, nil, err
	}
	if len(inputs) != len(definition.Partitions) {
		return nil, nil, fmt.Errorf("%w: incomplete trusted partition collection", ErrCorrupt)
	}
	files := make(map[string]PartitionInput, len(inputs))
	for _, in := range inputs {
		files[in.Name] = in
	}
	set := &PartitionSet{Database: definition.Database, Name: definition.Table, MetadataSource: "explicit_schema", PartitionType: definition.PartitionType, Expression: definition.Expression}
	ordered := make([]orderedPartition, len(inputs))
	for i, entry := range definition.Partitions {
		in, ok := files[entry.Name]
		if !ok {
			return nil, nil, fmt.Errorf("%w: missing partition %s", ErrCorrupt, entry.Name)
		}
		if err := scanEntries(reader, uint64(1+len(entry.Indexes))); err != nil {
			return nil, nil, err
		}
		selectPartition(reader, in)
		fsp, err := readPage(reader, in.Size, 0)
		if err != nil {
			return nil, nil, err
		}
		if be.Uint32(fsp[54:])&0x4000 != 0 {
			return nil, nil, fmt.Errorf("%w: 5.7 trusted partition contains SDI", ErrUnsupported)
		}
		if err := ValidateSchema(reader, in.Size, entry.Schema); err != nil {
			return nil, nil, fmt.Errorf("partition %s: %w", entry.Name, err)
		}
		if err := ValidateSecondarySchemas(reader, in.Size, entry.Schema, entry.Indexes); err != nil {
			return nil, nil, fmt.Errorf("partition %s: %w", entry.Name, err)
		}
		schema, err := copyPartitionSchema(entry.Schema)
		if err != nil {
			return nil, nil, err
		}
		meta := &TableMetadata{Database: definition.Database, Name: definition.Table, TableID: entry.TableID, MaterializedSchema: &schema, Issues: []string{}}
		if len(schema.VirtualColumns) == 0 {
			meta.Schema = &schema
		}
		source := PartitionSource{Name: entry.Name, Number: entry.Number, TableID: entry.TableID, SpaceID: schema.SpaceID}
		data, err := json.Marshal(entry.Indexes)
		if err != nil {
			return nil, nil, err
		}
		var indexes []SecondarySchema
		if err := json.Unmarshal(data, &indexes); err != nil {
			return nil, nil, err
		}
		set.Partitions = append(set.Partitions, PartitionMetadata{Source: source, Table: meta, Indexes: indexes})
		ordered[i] = orderedPartition{PartitionInput: in, tableID: entry.TableID}
	}
	set.Columns, set.VirtualColumns = set.Partitions[0].Table.MaterializedSchema.Columns, set.Partitions[0].Table.MaterializedSchema.VirtualColumns
	return set, ordered, nil
}
