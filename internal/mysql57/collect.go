package mysql57

import (
	"context"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Options struct {
	Host, Socket, User, PasswordEnv, TLSCA string
	Database, Table                        string
	Port                                   int
	Timeout                                time.Duration
	Indexes, TLS, Partitions               bool
}
type Bundle struct {
	Schema     innodb.Schema
	Indexes    []innodb.SecondarySchema
	Source     map[string]any
	Partitions *innodb.PartitionSchema
}
type environment struct {
	Version      string `json:"version"`
	PageSize     int    `json:"page_size"`
	FilePerTable int    `json:"file_per_table"`
	Checksum     string `json:"checksum"`
	Datadir      string `json:"datadir"`
	Charset      string `json:"charset"`
	Collation    string `json:"collation"`
	SQLMode      string `json:"sql_mode"`
	TimeZone     string `json:"time_zone"`
}
type column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	ColumnType string `json:"column_type"`
	Nullable   string `json:"nullable"`
	Charset    string `json:"charset"`
	Collation  string `json:"collation"`
	Chars      int    `json:"chars"`
	Bytes      int    `json:"bytes"`
	Precision  int    `json:"precision"`
	Scale      int    `json:"scale"`
	FSP        int    `json:"fsp"`
	Extra      string `json:"extra"`
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}
type index struct {
	Name    string `json:"name"`
	ID      uint64 `json:"index_id"`
	Space   uint32 `json:"space_id"`
	Root    uint32 `json:"root_page"`
	Type    int    `json:"type"`
	Fields  int    `json:"fields"`
	TableID uint64 `json:"table_id"`
}
type field struct {
	Name     string `json:"name"`
	Position int    `json:"position"`
}
type statistic struct {
	Name     string `json:"name"`
	Column   string `json:"column"`
	Position int    `json:"position"`
	Prefix   *int   `json:"prefix"`
	Order    string `json:"order"`
	Unique   bool   `json:"unique"`
	Type     string `json:"type"`
}
type tablespace struct {
	Flags       uint32 `json:"flags"`
	PageSize    int    `json:"page_size"`
	ZipPageSize int    `json:"zip_page_size"`
	SpaceType   string `json:"space_type"`
}
type datafile struct {
	Path string `json:"path"`
}
type dictionary struct {
	Columns int `json:"columns"`
}
type queryer interface {
	objects(string, string, any) error
}

func ValidateOptions(o Options) error {
	for _, value := range []string{o.Database, o.Table, o.User} {
		if value == "" || strings.ContainsRune(value, 0) {
			return fmt.Errorf("database, table and user must be nonempty and contain no NUL")
		}
	}
	for _, value := range []string{o.Host, o.Socket, o.PasswordEnv, o.TLSCA} {
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("connection options must contain no NUL")
		}
	}
	if o.Port < 0 || o.Port > 65535 {
		return fmt.Errorf("port must be 1..65535, or 0 for the default")
	}
	if o.Socket != "" && (o.Host != "" || o.Port != 0 || o.TLS || o.TLSCA != "") {
		return fmt.Errorf("--socket cannot be combined with TCP host/port or TLS options")
	}
	if o.TLSCA != "" && !o.TLS {
		return fmt.Errorf("--tls-ca requires --tls")
	}
	if strings.Contains(o.PasswordEnv, "=") {
		return fmt.Errorf("invalid password environment variable name")
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	return nil
}

// Collect reads definitions through one dedicated connection. The caller must
// prevent concurrent DDL and pair these definitions with a stable physical snapshot.
func Collect(ctx context.Context, o Options) (bundle *Bundle, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = ValidateOptions(o); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	c, err := newClient(ctx, o)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := c.finish(err); e != nil {
			err = e
			bundle = nil
		}
	}()
	bundle, err = collect(c, o)
	if err != nil {
		return nil, err
	}
	bundle.Source["capture"] = "metadata only; caller must prevent concurrent DDL and pair a stable snapshot"
	bundle.Source["connection"] = map[string]any{"network": c.network, "address": c.address, "user": o.User, "tls": o.TLS}

	return bundle, nil
}

func supportedVersion(version string) bool {
	switch strings.SplitN(version, "-", 2)[0] {
	case "5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43", "5.7.44":
		return true
	}
	return false
}

func collect(q queryer, o Options) (*Bundle, error) {
	if o.Partitions {
		return collectPartitions(q, o)
	}
	return collectTable(q, o, o.Database+"/"+o.Table)
}

func collectTable(q queryer, o Options, physicalName string) (*Bundle, error) {
	source := map[string]any{"database": o.Database, "table": o.Table}
	var env []environment
	if err := readObjects(q, source, "environment", "JSON_OBJECT('version',VERSION(),'page_size',@@innodb_page_size,'file_per_table',@@innodb_file_per_table,'checksum',@@innodb_checksum_algorithm,'datadir',@@datadir,'charset',@@character_set_server,'collation',@@collation_server,'sql_mode',@@sql_mode,'time_zone',@@time_zone)", "", &env); err != nil {
		return nil, err
	}
	if len(env) != 1 || !supportedVersion(env[0].Version) || env[0].PageSize != 16384 {
		return nil, fmt.Errorf("only MySQL 5.7.9/10/11/12/28/43/44 / 16 KiB is validated")
	}
	where := "WHERE TABLE_SCHEMA=" + literal(o.Database) + " AND TABLE_NAME=" + literal(o.Table)
	var tables []struct {
		Engine    string `json:"engine"`
		RowFormat string `json:"row_format"`
		Type      string `json:"type"`
	}
	if err := readObjects(q, source, "", "JSON_OBJECT('engine',ENGINE,'row_format',ROW_FORMAT,'type',TABLE_TYPE)", "FROM INFORMATION_SCHEMA.TABLES "+where, &tables); err != nil {
		return nil, err
	}
	if len(tables) != 1 || tables[0].Engine != "InnoDB" || tables[0].Type != "BASE TABLE" {
		return nil, fmt.Errorf("one InnoDB base table required")
	}
	form := strings.ToUpper(tables[0].RowFormat)
	if form != "DYNAMIC" && form != "COMPACT" {
		return nil, fmt.Errorf("only DYNAMIC/COMPACT supported")
	}
	var columns []column
	if err := readObjects(q, source, "columns", "JSON_OBJECT('name',COLUMN_NAME,'type',DATA_TYPE,'column_type',COLUMN_TYPE,'nullable',IS_NULLABLE,'charset',CHARACTER_SET_NAME,'collation',COLLATION_NAME,'chars',CHARACTER_MAXIMUM_LENGTH,'bytes',CHARACTER_OCTET_LENGTH,'precision',NUMERIC_PRECISION,'scale',NUMERIC_SCALE,'fsp',DATETIME_PRECISION,'extra',EXTRA,'expression',GENERATION_EXPRESSION,'ordinal',ORDINAL_POSITION)", "FROM INFORMATION_SCHEMA.COLUMNS "+where+" ORDER BY ORDINAL_POSITION", &columns); err != nil {
		return nil, err
	}
	var indexes []index
	if err := readObjects(q, source, "indexes", "JSON_OBJECT('name',i.NAME,'index_id',i.INDEX_ID,'space_id',i.SPACE,'root_page',i.PAGE_NO,'type',i.TYPE,'fields',i.N_FIELDS,'table_id',i.TABLE_ID)", "FROM INFORMATION_SCHEMA.INNODB_SYS_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_SYS_TABLES t ON t.TABLE_ID=i.TABLE_ID WHERE t.NAME="+literal(physicalName)+" ORDER BY i.INDEX_ID", &indexes); err != nil {
		return nil, err
	}
	var cluster *index
	for i := range indexes {
		idx := &indexes[i]
		if idx.Type < 0 || idx.Type > 3 {
			return nil, fmt.Errorf("FULLTEXT/SPATIAL/indexed VIRTUAL layouts unsupported")
		}
		if idx.Type&1 != 0 {
			if cluster != nil {
				return nil, fmt.Errorf("one private ordinary clustered index required")
			}
			cluster = idx
		}
	}
	if cluster == nil || cluster.Space == 0 {
		return nil, fmt.Errorf("one private ordinary clustered index required; partitions/shared spaces unsupported")
	}
	var dict []dictionary
	if err := readObjects(q, source, "dictionary", "JSON_OBJECT('columns',N_COLS)", fmt.Sprintf("FROM INFORMATION_SCHEMA.INNODB_SYS_TABLES WHERE TABLE_ID=%d", cluster.TableID), &dict); err != nil {
		return nil, err
	}
	var spaces []tablespace
	if err := readObjects(q, source, "tablespace", "JSON_OBJECT('flags',FLAG,'page_size',PAGE_SIZE,'zip_page_size',ZIP_PAGE_SIZE,'space_type',SPACE_TYPE)", fmt.Sprintf("FROM INFORMATION_SCHEMA.INNODB_SYS_TABLESPACES WHERE SPACE=%d", cluster.Space), &spaces); err != nil {
		return nil, err
	}
	if len(spaces) != 1 || spaces[0].SpaceType != "Single" || spaces[0].PageSize != 16384 || spaces[0].ZipPageSize != 0 || spaces[0].Flags & ^uint32(0x7e1) != 0 {
		return nil, fmt.Errorf("uncompressed unencrypted private 16 KiB space required")
	}
	var fields []field
	if err := readObjects(q, source, "clustered_fields", "JSON_OBJECT('name',NAME,'position',POS)", fmt.Sprintf("FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS WHERE INDEX_ID=%d ORDER BY POS", cluster.ID), &fields); err != nil {
		return nil, err
	}
	var stats []statistic
	if err := readObjects(q, source, "statistics", "JSON_OBJECT('name',INDEX_NAME,'column',COLUMN_NAME,'position',SEQ_IN_INDEX,'prefix',SUB_PART,'order',COLLATION,'unique',NON_UNIQUE=0,'type',INDEX_TYPE)", "FROM INFORMATION_SCHEMA.STATISTICS "+where+" ORDER BY INDEX_NAME,SEQ_IN_INDEX", &stats); err != nil {
		return nil, err
	}
	var files []datafile
	if err := readObjects(q, source, "datafiles", "JSON_OBJECT('path',PATH)", fmt.Sprintf("FROM INFORMATION_SCHEMA.INNODB_SYS_DATAFILES WHERE SPACE=%d", cluster.Space), &files); err != nil {
		return nil, err
	}
	b := &Bundle{Schema: innodb.Schema{RowFormat: form, RootPage: cluster.Root, SpaceID: cluster.Space, IndexID: cluster.ID}}
	for _, c := range columns {
		if strings.Contains(c.Extra, "VIRTUAL GENERATED") {
			b.Schema.VirtualColumns = append(b.Schema.VirtualColumns, innodb.VirtualColumn{Name: c.Name, Ordinal: c.Ordinal, Expression: c.Expression})
			continue
		}
		typ := strings.ToUpper(c.Type)
		if !slices.Contains(strings.Fields("TINYINT SMALLINT MEDIUMINT INT BIGINT DECIMAL FLOAT DOUBLE DATE YEAR DATETIME TIME TIMESTAMP BIT CHAR VARCHAR BINARY VARBINARY ENUM SET JSON TINYTEXT TEXT MEDIUMTEXT LONGTEXT TINYBLOB BLOB MEDIUMBLOB LONGBLOB GEOMETRY POINT LINESTRING POLYGON MULTIPOINT MULTILINESTRING MULTIPOLYGON GEOMETRYCOLLECTION"), typ) {
			return nil, fmt.Errorf("unsupported column type: %s", typ)
		}
		out := innodb.Column{Name: c.Name, Type: typ, Nullable: c.Nullable == "YES", Unsigned: slices.Contains(strings.Fields(c.ColumnType), "unsigned")}
		if c.Charset != "" {
			if !slices.Contains([]string{"utf8mb4", "utf8", "ascii", "latin1"}, c.Charset) {
				return nil, fmt.Errorf("unsupported charset: %s", c.Charset)
			}
			out.Charset = c.Charset
		}
		switch typ {
		case "CHAR", "VARCHAR":
			out.MaxChars = c.Chars
		case "BINARY", "VARBINARY":
			out.MaxBytes = c.Bytes
		case "DECIMAL":
			out.Precision = c.Precision
			out.Scale = c.Scale
		case "DATETIME", "TIME", "TIMESTAMP":
			out.FSP = c.FSP
		case "BIT":
			out.BitLength = c.Precision
		case "ENUM", "SET":
			pos := strings.IndexByte(c.ColumnType, '(')
			if pos < 0 {
				return nil, fmt.Errorf("missing enum/set declaration")
			}
			var values [][]string
			if err := readObjects(q, source, "", "JSON_ARRAY"+c.ColumnType[pos:], "", &values); err != nil {
				return nil, err
			}
			if len(values) != 1 {
				return nil, fmt.Errorf("missing enum/set labels")
			}
			if typ == "ENUM" {
				out.EnumValues = values[0]
			} else {
				out.SetValues = values[0]
			}
		}
		if strings.Contains(c.Extra, "STORED GENERATED") {
			out.GenerationExpression = c.Expression
		}
		b.Schema.Columns = append(b.Schema.Columns, out)
	}
	if len(dict) != 1 || dict[0].Columns != len(b.Schema.Columns)+3 {
		return nil, fmt.Errorf("internal stored columns are absent from SQL metadata")
	}
	var keys []string
	for _, k := range stats {
		if k.Name == cluster.Name {
			if k.Prefix != nil || k.Type != "BTREE" || k.Order != "A" {
				return nil, fmt.Errorf("complete ascending BTREE clustered key required")
			}
			keys = append(keys, k.Column)
		}
	}
	if cluster.Name == "GEN_CLUST_INDEX" && len(keys) == 0 && len(fields) == 0 {
		b.Schema.ClusteredKey = &innodb.ClusteredKey{Name: cluster.Name, HiddenRowID: true}
	} else {
		if len(keys) == 0 || len(fields) != len(keys) {
			return nil, fmt.Errorf("clustered dictionary and SQL key fields disagree")
		}
		for i := range keys {
			if fields[i].Name != keys[i] {
				return nil, fmt.Errorf("clustered dictionary and SQL key fields disagree")
			}
		}
		if cluster.Name == "PRIMARY" {
			b.Schema.PrimaryKeys = keys
		} else {
			b.Schema.ClusteredKey = &innodb.ClusteredKey{Name: cluster.Name, Columns: keys}
		}
		for i := range b.Schema.Columns {
			c := &b.Schema.Columns[i]
			if slices.Contains(keys, c.Name) && c.Charset != "" {
				for _, raw := range columns {
					if raw.Name == c.Name {
						var err error
						c.Collation, err = collation(raw.Collation)
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	b.Source = source
	for _, key := range []string{"environment", "dictionary", "tablespace"} {
		b.Source[key] = source[key].([]json.RawMessage)[0]
	}
	if o.Indexes {
		var err error
		b.Indexes, err = secondary(q, b, columns, indexes, stats)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Retain the exact SQL metadata JSON, including NULL attributes and uint64 IDs.
func readObjects(q queryer, source map[string]any, key, expression, tail string, value any) error {
	var rows []json.RawMessage
	if err := q.objects(expression, tail, &rows); err != nil {
		return err
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, value); err != nil {
		return err
	}
	if key != "" {
		source[key] = rows
	}
	return nil
}

func collation(value string) (string, error) {
	switch value {
	case "utf8mb4_bin", "ascii_bin", "latin1_bin":
		return value, nil
	case "utf8_bin":
		return "utf8mb3_bin", nil
	}
	return "", fmt.Errorf("unsupported key collation: %s", value)
}

func secondary(q queryer, b *Bundle, columns []column, indexes []index, stats []statistic) ([]innodb.SecondarySchema, error) {
	result := make([]innodb.SecondarySchema, 0)
	rawFields := map[string]any{}
	b.Source["secondary_fields"] = rawFields
	makeField := func(name string, prefix *int) (innodb.SecondaryField, error) {
		for i, c := range b.Schema.Columns {
			if c.Name != name {
				continue
			}
			f := innodb.SecondaryField{Column: i, Definition: c}
			if !slices.Contains(strings.Fields("TINYINT SMALLINT MEDIUMINT INT BIGINT DECIMAL BIT DATE YEAR DATETIME TIME TIMESTAMP CHAR VARCHAR BINARY VARBINARY"), c.Type) {
				return f, fmt.Errorf("unsupported secondary key type: %s", c.Type)
			}
			width := 1
			if c.Type == "CHAR" || c.Type == "VARCHAR" {
				for _, raw := range columns {
					if raw.Name == name {
						var err error
						f.Definition.Collation, err = collation(raw.Collation)
						if err != nil {
							return f, err
						}
					}
				}
				width = map[string]int{"utf8mb4": 4, "utf8": 3, "ascii": 1, "latin1": 1}[c.Charset]
			}
			if prefix != nil {
				maximum := c.MaxBytes
				if c.Type == "CHAR" || c.Type == "VARCHAR" {
					maximum = c.MaxChars
				}
				if *prefix <= 0 || *prefix > maximum || !slices.Contains([]string{"CHAR", "VARCHAR", "BINARY", "VARBINARY"}, c.Type) {
					return f, fmt.Errorf("unsupported prefix")
				}
				if *prefix < maximum {
					f.PrefixBytes = *prefix * width
				}
			}
			return f, nil
		}
		return innodb.SecondaryField{}, fmt.Errorf("indexed VIRTUAL/internal column unsupported: %s", name)
	}
	var locator []innodb.SecondaryField
	if b.Schema.ClusteredKey != nil && b.Schema.ClusteredKey.HiddenRowID {
		locator = []innodb.SecondaryField{{Column: -1, RowID: true, Definition: innodb.Column{Name: "DB_ROW_ID", Type: "BINARY", MaxBytes: 6}}}
	} else {
		keys := b.Schema.PrimaryKeys
		if b.Schema.ClusteredKey != nil {
			keys = b.Schema.ClusteredKey.Columns
		}
		for _, name := range keys {
			f, err := makeField(name, nil)
			if err != nil {
				return nil, err
			}
			locator = append(locator, f)
		}
	}
	for _, idx := range indexes {
		if idx.Type&1 != 0 {
			continue
		}
		var keys []statistic
		for _, s := range stats {
			if s.Name == idx.Name {
				keys = append(keys, s)
			}
		}
		var raw []field
		if err := readObjects(q, rawFields, idx.Name, "JSON_OBJECT('name',NAME,'position',POS)", fmt.Sprintf("FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS WHERE INDEX_ID=%d ORDER BY POS", idx.ID), &raw); err != nil {
			return nil, err
		}
		if len(keys) == 0 || len(keys) != idx.Fields || len(raw) != len(keys) {
			return nil, fmt.Errorf("secondary dictionary and SQL declarations disagree: %s", idx.Name)
		}
		s := innodb.SecondarySchema{Name: idx.Name, Unique: idx.Type == 2, RowFormat: b.Schema.RowFormat, SpaceID: idx.Space, IndexID: idx.ID, RootPage: idx.Root, UserFields: len(keys)}
		for i, k := range keys {
			if raw[i].Name != k.Column || k.Type != "BTREE" || k.Order != "A" || k.Position != i+1 || k.Unique != s.Unique {
				return nil, fmt.Errorf("secondary dictionary and SQL declarations disagree: %s", idx.Name)
			}
			f, err := makeField(k.Column, k.Prefix)
			if err != nil {
				return nil, err
			}
			s.Fields = append(s.Fields, f)
		}
		for _, f := range locator {
			found := -1
			for i, old := range s.Fields {
				if old.Column == f.Column && old.RowID == f.RowID && old.PrefixBytes == 0 {
					found = i
					break
				}
			}
			if found < 0 {
				found = len(s.Fields)
				s.Fields = append(s.Fields, f)
			}
			s.ClusteredFields = append(s.ClusteredFields, found)
		}
		result = append(result, s)
	}
	return result, nil
}

// WriteDefinitions preserves the reader's strict, at-most-1-MiB definition files.
func WriteDefinitions(stage string, b *Bundle, indexes bool) error {
	values := map[string]any{"schema.json": b.Schema, "source.json": b.Source}
	if b.Partitions != nil {
		values = map[string]any{"partitions.json": b.Partitions, "source.json": b.Source}
	} else if indexes {
		values["indexes.json"] = b.Indexes
	}
	for name, value := range values {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if name != "source.json" && len(data)+1 > 1<<20 {
			return fmt.Errorf("%s exceeds 1 MiB", name)
		}
		if err = os.WriteFile(filepath.Join(stage, name), append(data, '\n'), 0600); err != nil {
			return err
		}
	}
	return nil
}
