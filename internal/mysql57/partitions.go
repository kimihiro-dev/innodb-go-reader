package mysql57

import (
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"strings"
)

type partition struct {
	Name               *string `json:"name"`
	Sub                *string `json:"sub"`
	Number             uint32  `json:"ordinal"`
	Method, Expression *string
}

func collectPartitions(q queryer, o Options) (*Bundle, error) {
	source := map[string]any{"database": o.Database, "table": o.Table}
	var env []environment
	if err := readObjects(q, source, "environment", "JSON_OBJECT('version',VERSION(),'page_size',@@innodb_page_size,'file_per_table',@@innodb_file_per_table,'checksum',@@innodb_checksum_algorithm,'datadir',@@datadir,'charset',@@character_set_server,'collation',@@collation_server,'sql_mode',@@sql_mode,'time_zone',@@time_zone)", "", &env); err != nil {
		return nil, err
	}
	if len(env) != 1 || !supportedVersion(env[0].Version) || env[0].PageSize != 16384 {
		return nil, fmt.Errorf("only validated MySQL 5.7 patches / 16 KiB are supported")
	}
	var parts []partition
	tail := "FROM INFORMATION_SCHEMA.PARTITIONS WHERE TABLE_SCHEMA=" + literal(o.Database) + " AND TABLE_NAME=" + literal(o.Table) + " ORDER BY PARTITION_ORDINAL_POSITION,SUBPARTITION_ORDINAL_POSITION"
	expression := "JSON_OBJECT('name',PARTITION_NAME,'sub',SUBPARTITION_NAME,'ordinal',PARTITION_ORDINAL_POSITION,'method',PARTITION_METHOD,'expression',PARTITION_EXPRESSION)"
	if err := readObjects(q, source, "partition_directory", expression, tail, &parts); err != nil {
		return nil, err
	}
	if len(parts) == 0 || len(parts) > innodb.MaxPartitionFiles {
		return nil, fmt.Errorf("one complete ordinary partitioned table required")
	}
	method := map[string]uint32{"HASH": 1, "KEY": 3, "RANGE": 7, "LIST": 8}
	definition := &innodb.PartitionSchema{Version: 1, MySQLVersion: env[0].Version, Database: o.Database, Table: o.Table}
	for i, p := range parts {
		if p.Name == nil || *p.Name == "" || p.Sub != nil || p.Number != uint32(i+1) || p.Method == nil || method[*p.Method] == 0 || p.Expression == nil {
			return nil, fmt.Errorf("ordinary RANGE/LIST/HASH/KEY partition directory required; subpartitions and other methods unsupported")
		}
		if i == 0 {
			definition.PartitionType, definition.Expression = method[*p.Method], *p.Expression
		} else if method[*p.Method] != definition.PartitionType || *p.Expression != definition.Expression {
			return nil, fmt.Errorf("partition method/expression mismatch")
		}
	}
	logical := o.Database + "/" + o.Table
	var physical []struct {
		Name string
		ID   uint64 `json:"table_id"`
	}
	prefix := logical + "#P#"
	if err := readObjects(q, source, "physical_tables", "JSON_OBJECT('name',NAME,'table_id',TABLE_ID)", "FROM INFORMATION_SCHEMA.INNODB_SYS_TABLES WHERE LEFT(NAME,CHAR_LENGTH("+literal(prefix)+")) IN("+literal(prefix)+","+literal(logical+"#p#")+") ORDER BY TABLE_ID", &physical); err != nil {
		return nil, err
	}
	names := map[string]struct {
		name string
		id   uint64
	}{}
	for _, p := range physical {
		name := strings.TrimPrefix(p.Name, prefix)
		if name == p.Name {
			name = strings.TrimPrefix(p.Name, logical+"#p#")
		}
		name = strings.ToLower(name)
		if name == p.Name || names[name].name != "" {
			return nil, fmt.Errorf("ambiguous physical partition names")
		}
		names[name] = struct {
			name string
			id   uint64
		}{p.Name, p.ID}
	}
	if len(names) != len(parts) {
		return nil, fmt.Errorf("physical partition directory mismatch; filename-encoded identifiers unsupported by online collection")
	}
	partSources := map[string]any{}
	source["partitions"] = partSources
	for i, p := range parts {
		identity, ok := names[strings.ToLower(*p.Name)]
		if !ok {
			return nil, fmt.Errorf("missing physical partition %s; filename-encoded identifiers unsupported by online collection", *p.Name)
		}
		b, err := collectTable(q, o, identity.name)
		if err != nil {
			return nil, fmt.Errorf("partition %s: %w", *p.Name, err)
		}
		definition.Partitions = append(definition.Partitions, innodb.PartitionSchemaEntry{Name: *p.Name, Number: uint32(i), TableID: identity.id, Schema: b.Schema, Indexes: b.Indexes})
		b.Source["physical_name"] = identity.name
		partSources[*p.Name] = b.Source
	}
	if err := innodb.ValidatePartitionSchema(*definition); err != nil {
		return nil, err
	}
	// Catch directory changes during metadata collection; callers still prevent DDL.
	var after []partition
	if err := q.objects(expression, tail, &after); err != nil {
		return nil, err
	}
	beforeJSON, _ := json.Marshal(parts)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		return nil, fmt.Errorf("partition directory changed during collection")
	}
	source["environment"] = source["environment"].([]json.RawMessage)[0]
	return &Bundle{Partitions: definition, Source: source}, nil
}
