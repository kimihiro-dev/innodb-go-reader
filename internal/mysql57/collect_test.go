package mysql57

import (
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The responses and expected definitions come from independent, fixed SQL captures.
type fixtureQueries struct {
	source map[string]json.RawMessage
	schema innodb.Schema
}

func (q fixtureQueries) objects(expression, tail string, out any) error {
	var raw json.RawMessage
	single := false
	switch {
	case strings.Contains(expression, "VERSION()"):
		raw, single = q.source["environment"], true
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.TABLES "):
		value := []map[string]string{{"engine": "InnoDB", "row_format": q.schema.RowFormat, "type": "BASE TABLE"}}
		b, _ := json.Marshal(value)
		raw = b
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.COLUMNS "):
		raw = q.source["columns"]
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_INDEXES "):
		raw = q.source["indexes"]
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_TABLES "):
		raw, single = q.source["dictionary"], true
		// Early A captures predate the hidden-column guard. Supply that one
		// response synthetically; B captures contain the independent SQL count.
		if raw == nil {
			raw, _ = json.Marshal(dictionary{Columns: len(q.schema.Columns) + 3})
		}
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_TABLESPACES "):
		raw, single = q.source["tablespace"], true
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS "):
		var id uint64
		if _, err := fmt.Sscanf(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS WHERE INDEX_ID=%d ORDER BY POS", &id); err != nil {
			return err
		}
		if id == q.schema.IndexID {
			raw = q.source["clustered_fields"]
		} else {
			var indexes []index
			json.Unmarshal(q.source["indexes"], &indexes)
			var fields map[string]json.RawMessage
			json.Unmarshal(q.source["secondary_fields"], &fields)
			for _, idx := range indexes {
				if idx.ID == id {
					raw = fields[idx.Name]
				}
			}
		}
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.STATISTICS "):
		raw = q.source["statistics"]
	case strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_DATAFILES "):
		raw = q.source["datafiles"]
	case strings.HasPrefix(expression, "JSON_ARRAY("):
		var columns []column
		json.Unmarshal(q.source["columns"], &columns)
		for _, c := range columns {
			pos := strings.IndexByte(c.ColumnType, '(')
			if pos < 0 || expression != "JSON_ARRAY"+c.ColumnType[pos:] {
				continue
			}
			for _, definition := range q.schema.Columns {
				if definition.Name == c.Name {
					labels := definition.EnumValues
					if definition.Type == "SET" {
						labels = definition.SetValues
					}
					b, _ := json.Marshal([][]string{labels})
					raw = b
				}
			}
		}
	}
	if raw == nil {
		return fmt.Errorf("unexpected SQL metadata query: %s %s", expression, tail)
	}
	if single {
		raw = append(append([]byte{'['}, raw...), ']')
	}
	return json.Unmarshal(raw, out)
}

func loadFixture(t *testing.T, source string) fixtureQueries {
	t.Helper()
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	q := fixtureQueries{}
	if err = json.Unmarshal(b, &q.source); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(strings.TrimSuffix(source, ".source.json") + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &q.schema); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCollectFixedSQLMetadata(t *testing.T) {
	dirs := []string{"mysql5744", "mysql5744-secondary"}
	for _, version := range []string{"5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43"} {
		dirs = append(dirs, "mysql57-patches/"+version+"/base", "mysql57-patches/"+version+"/secondary")
	}
	for _, dir := range dirs {
		files, err := filepath.Glob("../../testdata/" + dir + "/*.source.json")
		if err != nil || len(files) == 0 {
			t.Fatal(err)
		}
		for _, file := range files {
			t.Run(dir+"/"+filepath.Base(file), func(t *testing.T) {
				q := loadFixture(t, file)
				var database, table string
				json.Unmarshal(q.source["database"], &database)
				json.Unmarshal(q.source["table"], &table)
				indexes := dir == "mysql5744-secondary" || strings.HasSuffix(dir, "/secondary")
				b, err := collect(q, Options{Database: database, Table: table, Indexes: indexes})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(b.Schema, q.schema) {
					t.Fatalf("Schema differs from independent capture\ngot %#v\nwant %#v", b.Schema, q.schema)
				}
				if indexes {
					var want []innodb.SecondarySchema
					raw, err := os.ReadFile(strings.TrimSuffix(file, ".source.json") + ".indexes.json")
					if err != nil || json.Unmarshal(raw, &want) != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(b.Indexes, want) {
						t.Fatal("physical secondary definition differs from independent SQL capture")
					}
				}
				// Source records retain SQL NULL instead of converting it to zero/empty text.
				for _, key := range []string{"environment", "columns", "indexes", "dictionary", "tablespace", "clustered_fields", "statistics", "datafiles"} {
					if q.source[key] == nil {
						continue
					}
					raw, _ := json.Marshal(b.Source[key])
					var got, want any
					json.Unmarshal(raw, &got)
					json.Unmarshal(q.source[key], &want)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("source %s changed", key)
					}
				}
				stage := t.TempDir()
				if err = WriteDefinitions(stage, b, indexes); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCollectRejectsIncompatibleMetadata(t *testing.T) {
	file := "../../testdata/mysql5744-secondary/ordinary_dynamic.source.json"
	for _, test := range []struct{ key, value string }{
		{"environment", `{"version":"8.0.45","page_size":16384}`},
		{"environment", `{"version":"5.7.44","page_size":8192}`},
		{"dictionary", `{"columns":999}`},
		{"tablespace", `{"flags":33,"page_size":16384,"zip_page_size":0,"space_type":"General"}`},
		{"tablespace", `{"flags":8225,"page_size":16384,"zip_page_size":0,"space_type":"Single"}`},
		{"tablespace", `{"flags":33,"page_size":16384,"zip_page_size":8192,"space_type":"Single"}`},
		{"indexes", `[{"name":"PRIMARY","space_id":0,"index_id":1,"root_page":3,"type":3}]`},
		{"indexes", `[{"name":"fts","space_id":1,"index_id":1,"root_page":3,"type":32}]`},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			q := loadFixture(t, file)
			q.source[test.key] = json.RawMessage(test.value)
			if _, err := collect(q, Options{Indexes: true}); err == nil {
				t.Fatal("accepted incompatible SQL metadata")
			}
		})
	}
	q := loadFixture(t, file)
	var env map[string]any
	json.Unmarshal(q.source["environment"], &env)
	env["version"] = "5.7.44-log"
	raw, _ := json.Marshal(env)
	q.source["environment"] = raw
	if _, err := collect(q, Options{Indexes: true}); err != nil {
		t.Fatal("valid version suffix", err)
	}
	if ident("a`b") != "`a``b`" || literal("界'") != "CONVERT(X'e7958c27' USING utf8mb4)" {
		t.Fatal("identifier/literal escaping")
	}
}

func TestSupportedPatchVersions(t *testing.T) {
	for _, version := range []string{"5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43", "5.7.44", "5.7.9-log", "5.7.43-log"} {
		if !supportedVersion(version) {
			t.Fatalf("validated version rejected: %s", version)
		}
	}
	for _, version := range []string{"", "5.7", "5.7.8", "5.7.13", "5.7.27", "5.7.42", "5.7.45", "5.7.09", "5.7.440", "8.0.45", "5.6.51", "10.6.0-MariaDB"} {
		if supportedVersion(version) {
			t.Fatalf("unvalidated version accepted: %s", version)
		}
	}
}
