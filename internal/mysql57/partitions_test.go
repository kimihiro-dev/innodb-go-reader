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

type partitionFixtureQueries struct {
	directory json.RawMessage
	physical  json.RawMessage
	queries   map[string]fixtureQueries
	current   fixtureQueries
}

func (q *partitionFixtureQueries) objects(expression, tail string, out any) error {
	if strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.PARTITIONS ") {
		return json.Unmarshal(q.directory, out)
	}
	if strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_TABLES WHERE LEFT") {
		return json.Unmarshal(q.physical, out)
	}
	if strings.HasPrefix(tail, "FROM INFORMATION_SCHEMA.INNODB_SYS_INDEXES ") {
		found := false
		for name, f := range q.queries {
			if strings.Contains(tail, "t.NAME="+literal(name)+" ORDER BY") {
				q.current = f
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unexpected physical binding %s", tail)
		}
	}
	return q.current.objects(expression, tail, out)
}

func TestCollectPartitionFixtures(t *testing.T) {
	for _, version := range []string{"5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43", "5.7.44"} {
		dir := filepath.Join("../../testdata/partition-patches", version)
		var manifest struct {
			Cases []struct {
				Name      string
				Supported bool
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		for _, c := range manifest.Cases {
			if !c.Supported {
				continue
			}
			t.Run(version+"/"+c.Name, func(t *testing.T) {
				var definition innodb.PartitionSchema
				raw, err := os.ReadFile(filepath.Join(dir, c.Name+".partitions.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &definition); err != nil {
					t.Fatal(err)
				}
				var source struct {
					Directory json.RawMessage
					Physical  []struct {
						Name string
						ID   uint64 `json:"table_id"`
					}
					Partitions map[string]map[string]json.RawMessage
				}
				raw, err = os.ReadFile(filepath.Join(dir, c.Name+".source.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &source); err != nil {
					t.Fatal(err)
				}
				q := &partitionFixtureQueries{directory: source.Directory, queries: map[string]fixtureQueries{}}
				var physical []map[string]any
				for _, p := range source.Physical {
					physical = append(physical, map[string]any{"name": p.Name, "table_id": p.ID})
					for _, entry := range definition.Partitions {
						if entry.TableID == p.ID {
							q.queries[p.Name] = fixtureQueries{source: source.Partitions[entry.Name], schema: entry.Schema}
							q.current = q.queries[p.Name]
						}
					}
				}
				q.physical, _ = json.Marshal(physical)
				b, err := collect(q, Options{Database: definition.Database, Table: definition.Table, Partitions: true, Indexes: true})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(b.Partitions, &definition) {
					t.Fatal("driver definition differs from independent SQL schema")
				}
				if err := WriteDefinitions(t.TempDir(), b, true); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
