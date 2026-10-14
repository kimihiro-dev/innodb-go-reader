package innodb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

type orderedPartition struct {
	PartitionInput
	tableID uint64
}
type ddPartition struct {
	Name          string            `json:"name"`
	Number        uint32            `json:"number"`
	ID            uint64            `json:"se_private_id"`
	Parent        uint64            `json:"parent_partition_id"`
	Engine        string            `json:"engine"`
	Private       string            `json:"se_private_data"`
	Indexes       []json.RawMessage `json:"indexes"`
	Subpartitions []json.RawMessage `json:"subpartitions"`
}
type ddPartitionIndex struct {
	Index   int    `json:"index_opx"`
	Private string `json:"se_private_data"`
	Space   string `json:"tablespace_ref"`
}
type partitionSDI struct {
	input PartitionInput
	sdi   *SDIResult
	space ddSpace
}

func copyPartitionSchema(s Schema) (Schema, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return Schema{}, err
	}
	var out Schema
	err = json.Unmarshal(b, &out)
	return out, err
}

func inspectPartitionSet(reader *scanReader, inputs []PartitionInput) (*PartitionSet, []orderedPartition, error) {
	fail := func(s string) (*PartitionSet, []orderedPartition, error) {
		return nil, nil, fmt.Errorf("%w: partition collection: %s", ErrCorrupt, s)
	}
	files := make(map[string]partitionSDI, len(inputs))
	spaces := map[uint32]bool{}
	var anchor *SDIRecord
	var anchorEnvelope ddEnvelope
	anchorName := ""
	total := 0
	for _, in := range inputs {
		if err := scanEntries(reader, 1); err != nil {
			return nil, nil, err
		}
		selectPartition(reader, in)
		sdi, err := ReadSDI(reader, in.Size)
		if err != nil {
			return nil, nil, fmt.Errorf("partition %s SDI: %w", in.Name, err)
		}
		if spaces[sdi.SpaceID] {
			return fail("duplicate physical space")
		}
		spaces[sdi.SpaceID] = true
		f := partitionSDI{input: in, sdi: &SDIResult{SpaceID: sdi.SpaceID}}
		spaceFound := false
		for _, rec := range sdi.DeletedRecords {
			if len(rec.JSON) > MaxSDITotalBytes-total {
				return nil, nil, fmt.Errorf("%w: collection SDI bytes", ErrLimit)
			}
			total += len(rec.JSON)
		}
		for _, rec := range sdi.Records {
			if len(rec.JSON) > MaxSDITotalBytes-total {
				return nil, nil, fmt.Errorf("%w: collection SDI bytes", ErrLimit)
			}
			total += len(rec.JSON)
			var e ddEnvelope
			if err = decodeMetadata(rec.JSON, &e, "mysqld_version_id dd_version sdi_version dd_object_type dd_object"); err != nil {
				return nil, nil, err
			}
			if !supportedSDIVersion(e) {
				return nil, nil, fmt.Errorf("%w: partition SDI version", ErrUnsupported)
			}
			switch e.Kind {
			case "Table":
				if anchor != nil || rec.Key.Type != 1 || rec.Key.ID == 0 {
					return fail("expected one logical Table SDI source")
				}
				copy := rec
				anchor = &copy
				anchorEnvelope = e
				anchorName = in.Name
			case "Tablespace":
				if spaceFound || rec.Key.Type != 2 {
					return fail("ambiguous Tablespace SDI")
				}
				spaceFound = true
				if err = decodeMetadata(e.Object, &f.space, "name engine se_private_data options files"); err != nil {
					return nil, nil, err
				}
				f.sdi.Records = append(f.sdi.Records, rec)
			default:
				return nil, nil, fmt.Errorf("%w: unknown partition SDI object", ErrUnsupported)
			}
		}
		if !spaceFound {
			return fail("missing Tablespace SDI")
		}
		files[in.Name] = f
	}
	if anchor == nil {
		return fail("missing first partition Table SDI")
	}
	var table ddTable
	if err := decodeMetadata(anchorEnvelope.Object, &table, "name schema_ref se_private_id engine row_format hidden partition_type subpartition_type partitions se_private_data options columns indexes"); err != nil {
		return nil, nil, err
	}
	if table.Subpartition != 0 {
		return nil, nil, fmt.Errorf("%w: subpartitions", ErrUnsupported)
	}
	if table.Partition != 1 && table.Partition != 3 && table.Partition != 7 && table.Partition != 8 {
		return nil, nil, fmt.Errorf("%w: partition method %d", ErrUnsupported, table.Partition)
	}
	if table.ID != ^uint64(0) || table.Engine != "InnoDB" {
		return fail("logical partition table identity")
	}
	if len(table.Partitions) != len(files) {
		return fail("missing or extra partition files")
	}
	if len(table.Indexes) == 0 {
		return fail("missing logical indexes")
	}
	// Keep original logical attributes. Only replace the documented physical
	// identity/index properties and remove the now-resolved partition wrapper.
	var logical metadataObject
	if err := json.Unmarshal(anchorEnvelope.Object, &logical); err != nil {
		return nil, nil, err
	}
	var expression string
	if err := json.Unmarshal(logical["partition_expression"], &expression); err != nil {
		return nil, nil, fmt.Errorf("%w: partition expression", ErrCorrupt)
	}
	set := &PartitionSet{Database: table.Database, Name: table.Name, Source: anchor.Key, PartitionType: table.Partition, Expression: expression}
	ordered := make([]orderedPartition, len(files))
	parts := make([]ddPartition, len(files))
	names := map[string]bool{}
	ids := map[uint64]bool{}
	positions := map[uint32]bool{}
	for _, raw := range table.Partitions {
		if err := scanEntries(reader, 1); err != nil {
			return nil, nil, err
		}
		var p ddPartition
		required := "name number se_private_id parent_partition_id engine se_private_data indexes subpartitions"
		if anchorEnvelope.MySQL < 80019 {
			required = "name number se_private_id parent_partition_id engine se_private_data indexes"
		}
		if err := decodeMetadata(raw, &p, required); err != nil {
			return nil, nil, err
		}
		if len(p.Subpartitions) > 0 {
			return nil, nil, fmt.Errorf("%w: nested partitions", ErrUnsupported)
		}
		if !utf8.ValidString(p.Name) || p.Name == "" || names[p.Name] || p.Number >= uint32(len(parts)) || positions[p.Number] || p.ID == 0 || p.ID == ^uint64(0) || ids[p.ID] || p.Parent != ^uint64(0) || p.Engine != "InnoDB" {
			return fail("invalid partition directory")
		}
		if _, ok := files[p.Name]; !ok {
			return fail("missing partition " + p.Name)
		}
		names[p.Name] = true
		ids[p.ID] = true
		positions[p.Number] = true
		parts[p.Number] = p
	}
	if parts[0].Name != anchorName {
		return fail("Table SDI is not in the first partition")
	}
	columnOwner := parts[0].ID
	if anchorEnvelope.MySQL == 80011 || anchorEnvelope.MySQL == 80012 {
		// Early creation/partial rebuild writes the shared owner from a
		// visited partition, rather than always from the first partition.
		if len(table.Columns) == 0 {
			return fail("missing shared columns")
		}
		var column ddColumn
		if err := json.Unmarshal(table.Columns[0], &column); err != nil {
			return nil, nil, err
		}
		properties, err := metadataProperties(column.Private)
		if err != nil {
			return nil, nil, err
		}
		columnOwner, err = propertyUint(properties, "table_id", 64)
		if err != nil {
			return nil, nil, err
		}
		if !ids[columnOwner] {
			return fail("shared column owner is absent from partition directory")
		}
	}
	for i, p := range parts {
		f := files[p.Name]
		if len(p.Indexes) != len(table.Indexes) {
			return fail("partition index count")
		}
		indexes := make([]json.RawMessage, len(table.Indexes))
		seen := map[int]bool{}
		for _, raw := range p.Indexes {
			if err := scanEntries(reader, 1); err != nil {
				return nil, nil, err
			}
			var idx ddPartitionIndex
			if err := decodeMetadata(raw, &idx, "index_opx se_private_data tablespace_ref"); err != nil {
				return nil, nil, err
			}
			if idx.Index < 0 || idx.Index >= len(indexes) || seen[idx.Index] || idx.Space != f.space.Name {
				return fail("partition index mapping/tablespace")
			}
			seen[idx.Index] = true
			var definition metadataObject
			if err := json.Unmarshal(table.Indexes[idx.Index], &definition); err != nil {
				return nil, nil, err
			}
			// The logical index has no physical properties; accepting conflicting ones
			// would hide a mixed or unsupported layout during adaptation.
			var original *string
			if err := json.Unmarshal(definition["se_private_data"], &original); err != nil || original == nil || *original != "" {
				return fail("logical index has physical properties")
			}
			if raw, present := definition["tablespace_ref"]; present {
				if err := json.Unmarshal(raw, &original); err != nil || original == nil || *original != "" {
					return fail("logical index has a physical tablespace")
				}
			}
			definition["se_private_data"], _ = json.Marshal(idx.Private)
			definition["tablespace_ref"], _ = json.Marshal(idx.Space)
			b, err := json.Marshal(definition)
			if err != nil {
				return nil, nil, err
			}
			indexes[idx.Index] = b
		}
		adapted := make(metadataObject, len(logical))
		for k, v := range logical {
			adapted[k] = v
		}
		adapted["partition_type"] = json.RawMessage("0")
		adapted["subpartition_type"] = json.RawMessage("0")
		adapted["partitions"] = json.RawMessage("[]")
		adapted["se_private_id"], _ = json.Marshal(p.ID)
		// Partition-level private attributes describe this physical table. Preserve
		// logical properties too and reject conflicts rather than silently replacing.
		tp, err := metadataProperties(table.Private)
		if err != nil {
			return nil, nil, err
		}
		pp, err := metadataProperties(p.Private)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range pp {
			if k != "autoinc" && k != "version" && k != "instant_col" {
				return nil, nil, fmt.Errorf("%w: partition private attribute %s", ErrUnsupported, k)
			}
			if old, ok := tp[k]; ok && old != v && k != "instant_col" {
				return fail("conflicting table/partition private properties")
			}
			if k != "instant_col" {
				tp[k] = v
			}
		}
		if _, legacy := tp["instant_col"]; legacy {
			minimum, err := propertyUint(tp, "instant_col", 16)
			if err != nil {
				return nil, nil, err
			}
			columns := append([]json.RawMessage(nil), table.Columns...)
			stored := uint64(0)
			for _, raw := range columns {
				var c ddColumn
				if err := json.Unmarshal(raw, &c); err != nil {
					return nil, nil, err
				}
				if !c.Virtual && c.Hidden != 2 {
					stored++
				}
			}
			original := stored
			if _, ok := pp["instant_col"]; ok {
				original, err = propertyUint(pp, "instant_col", 16)
				if err != nil {
					return nil, nil, err
				}
			}
			if minimum >= stored || original < minimum || original > stored {
				return fail("partition INSTANT column count")
			}
			if _, ok := pp["instant_col"]; ok {
				tp["instant_col"] = pp["instant_col"]
			} else {
				delete(tp, "instant_col")
			}
			position := uint64(0)
			for j, raw := range columns {
				var c ddColumn
				if err := json.Unmarshal(raw, &c); err != nil {
					return nil, nil, err
				}
				if c.Virtual || c.Hidden == 2 {
					continue
				}
				if position >= minimum && position < original {
					properties, err := metadataProperties(c.Private)
					if err != nil {
						return nil, nil, err
					}
					delete(properties, "default")
					delete(properties, "default_null")
					var object metadataObject
					if err := json.Unmarshal(raw, &object); err != nil {
						return nil, nil, err
					}
					object["se_private_data"], _ = json.Marshal(partitionProperties(properties))
					columns[j], err = json.Marshal(object)
					if err != nil {
						return nil, nil, err
					}
				}
				position++
			}
			adapted["columns"], _ = json.Marshal(columns)
		} else if _, ok := pp["instant_col"]; ok {
			return fail("partition INSTANT lacks logical minimum")
		}
		adapted["se_private_data"], _ = json.Marshal(partitionProperties(tp))
		adapted["indexes"], _ = json.Marshal(indexes)
		object, err := json.Marshal(adapted)
		if err != nil {
			return nil, nil, err
		}
		// Preserve version and all envelope attributes while replacing only dd_object.
		var envelope metadataObject
		if err = json.Unmarshal(anchor.JSON, &envelope); err != nil {
			return nil, nil, err
		}
		envelope["dd_object"] = object
		adaptedRecord := *anchor
		adaptedRecord.JSON, err = json.Marshal(envelope)
		if err != nil {
			return nil, nil, err
		}
		synthetic := *f.sdi
		synthetic.Records = append(append([]SDIRecord{}, f.sdi.Records...), adaptedRecord)
		selectPartition(reader, f.input)
		meta, err := inspectSDITableWithColumnOwner(reader, f.input.Size, &synthetic, columnOwner)
		if err != nil {
			return nil, nil, fmt.Errorf("partition %s: %w", p.Name, err)
		}
		if meta.MaterializedSchema == nil {
			return nil, nil, unsupportedPartition(meta)
		}
		if i == 0 {
			set.Columns = meta.MaterializedSchema.Columns
			set.VirtualColumns = meta.MaterializedSchema.VirtualColumns
		} else if !reflect.DeepEqual(set.Columns, meta.MaterializedSchema.Columns) || !reflect.DeepEqual(set.VirtualColumns, meta.MaterializedSchema.VirtualColumns) {
			return fail("logical column mismatch")
		}
		source := PartitionSource{Name: p.Name, Number: p.Number, TableID: p.ID, SpaceID: f.sdi.SpaceID}
		set.Partitions = append(set.Partitions, PartitionMetadata{Source: source, Table: meta})
		ordered[i] = orderedPartition{PartitionInput: f.input, tableID: p.ID}
	}
	return set, ordered, nil
}

// Preserve escaped property values while adapting only documented attributes.
func partitionProperties(properties map[string]string) string {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	escape := strings.NewReplacer("\\", "\\\\", ";", "\\;", "=", "\\=")
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(escape.Replace(key))
		out.WriteByte('=')
		out.WriteString(escape.Replace(properties[key]))
		out.WriteByte(';')
	}
	return out.String()
}
