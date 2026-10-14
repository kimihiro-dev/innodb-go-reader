package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

const mysql57SecondaryFixtures = "testdata/mysql5744-secondary"

func mysql57SecondaryFixture(t testing.TB, name string) ([]byte, Schema, []SecondarySchema) {
	t.Helper()
	b := unzip(t, mysql57SecondaryFixtures+"/"+name+".ibd.gz")
	var table Schema
	var indexes []SecondarySchema
	for path, out := range map[string]any{name + ".json": &table, name + ".indexes.json": &indexes} {
		raw, err := os.ReadFile(mysql57SecondaryFixtures + "/" + path)
		if err != nil || json.Unmarshal(raw, out) != nil {
			t.Fatal("metadata", path, err)
		}
	}
	return b, table, indexes
}
func mysql57SecondaryKey(t testing.TB, raw []json.RawMessage, index SecondarySchema) Key {
	t.Helper()
	key := Key{}
	for i, b := range raw {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var value any
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if s, ok := value.(string); ok && (index.Fields[i].Definition.Type == "BINARY" || index.Fields[i].Definition.Type == "VARBINARY") {
			decoded, err := hex.DecodeString(s)
			if err != nil {
				t.Fatal(err)
			}
			value = decoded
		}
		key = append(key, value)
	}
	return key
}
func mysql57EqualRows(t testing.TB, got []string, raw []json.RawMessage, unordered bool) {
	t.Helper()
	want := []string{}
	for _, row := range raw {
		want = append(want, mysql57Canonical(t, row))
	}
	if unordered {
		sort.Strings(got)
		sort.Strings(want)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SQL rows differ: got %d want %d\ngot %v\nwant %v", len(got), len(want), got, want)
	}
}
func TestMySQL57SecondaryFixtures(t *testing.T) {
	raw, err := os.ReadFile(mysql57SecondaryFixtures + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Status string
		Cases  []struct {
			Name, SHA256  string
			Rows, Indexes int
			Unordered     bool
		}
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Status != "captured" || len(manifest.Cases) != 22 {
		t.Fatal("manifest")
	}
	var totalRows, totalIndexes, totalQueries int
	var covered, lookups, filtered uint64
	var deep bool
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, table, indexes := mysql57SecondaryFixture(t, c.Name)
			r := bytes.NewReader(b)
			size := int64(len(b))
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 || len(indexes) != c.Indexes {
				t.Fatal("identity")
			}
			if err := ValidateSchema(r, size, table); err != nil {
				t.Fatal(err)
			}
			if err := ValidateSecondarySchemas(r, size, table, indexes); err != nil {
				t.Fatal(err)
			}
			rows, err := ReadMaterialized(r, size, table)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, row := range rows.Result.Records {
				got = append(got, mysql57Row(t, row.Values))
			}
			var expected []json.RawMessage
			if err := json.Unmarshal(unzip(t, mysql57SecondaryFixtures+"/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			mysql57EqualRows(t, got, expected, c.Unordered)
			totalRows += len(got)
			var sql []struct {
				Index   string
				Entries []json.RawMessage
				Queries []struct {
					Label                       string
					Columns                     []string
					Prefix, Point, Lower, Upper []json.RawMessage
					Reverse                     bool
					Limit                       uint64
					Rows                        []json.RawMessage
				}
			}
			if err := json.Unmarshal(unzip(t, mysql57SecondaryFixtures+"/"+c.Name+".secondary.json.gz"), &sql); err != nil {
				t.Fatal(err)
			}
			for i, index := range indexes {
				t.Run(index.Name, func(t *testing.T) {
					result, err := ReadSecondary(r, size, index)
					if err != nil {
						t.Fatal(err)
					}
					deep = deep || result.Page.Level >= 2
					if sql[i].Index != index.Name || len(result.Records) != c.Rows {
						t.Fatal("index/row count")
					}
					got := []string{}
					for _, entry := range result.Records {
						values := []any{}
						for j, v := range entry.Values {
							if !index.Fields[j].RowID {
								values = append(values, v)
							}
						}
						if table.hiddenRowID() && entry.RowID == nil {
							t.Fatal("missing ROW_ID")
						}
						got = append(got, mysql57Row(t, values))
					}
					mysql57EqualRows(t, got, sql[i].Entries, c.Unordered)
					streamed := []string{}
					report, err := ScanSecondary(t.Context(), r, size, index, ScanOptions{}, func(e SecondaryEvent) error {
						if e.Record != nil {
							streamed = append(streamed, mysql57Row(t, e.Record.Values))
						}
						return nil
					})
					if err != nil || !report.Complete || report.Records != uint64(c.Rows) {
						t.Fatal(err, report)
					}
					for j, record := range result.Records {
						if streamed[j] != mysql57Row(t, record.Values) {
							t.Fatal("stream/read")
						}
					}
					for _, q := range sql[i].Queries {
						t.Run(q.Label, func(t *testing.T) {
							query := SecondaryQuery{Columns: q.Columns, Range: KeyRange{Reverse: q.Reverse, Limit: q.Limit}}
							if len(q.Prefix) > 0 {
								query.Range.Prefix = mysql57SecondaryKey(t, q.Prefix, index)
							}
							if len(q.Lower) > 0 {
								query.Range.Lower = &KeyBound{Key: mysql57SecondaryKey(t, q.Lower, index), Inclusive: false}
								query.Range.Upper = &KeyBound{Key: mysql57SecondaryKey(t, q.Upper, index), Inclusive: true}
							}
							if len(q.Point) > 0 {
								query.Range = PointKey(mysql57SecondaryKey(t, q.Point, index))
							}
							got := []string{}
							report, err := QuerySecondaryMaterialized(t.Context(), r, size, table, index, query, ScanOptions{}, func(p ProjectedRow) error {
								if p.Covered && p.ClusteredPage != nil || !p.Covered && p.ClusteredPage == nil {
									t.Fatal("projection provenance")
								}
								got = append(got, mysql57Row(t, p.Values))
								return nil
							})
							if err != nil || !report.Complete {
								t.Fatal(err, report)
							}
							mysql57EqualRows(t, got, q.Rows, c.Unordered)
							covered += report.Covered
							lookups += report.Lookups
							filtered += report.Filtered
							totalQueries++
						})
					}
					totalIndexes++
				})
			}
		})
	}
	if totalRows != 1682 || totalIndexes != 38 || !deep || covered == 0 || lookups == 0 || filtered == 0 {
		t.Fatal("coverage", totalRows, totalIndexes, totalQueries, deep, covered, lookups, filtered)
	}
	t.Logf("SQL: %d rows, %d indexes, %d queries; covered=%d lookups=%d filtered=%d", totalRows, totalIndexes, totalQueries, covered, lookups, filtered)
}
func TestMySQL57SecondarySchemaFailures(t *testing.T) {
	b, table, indexes := mysql57SecondaryFixture(t, "ordinary_dynamic")
	size := int64(len(b))
	for name, mutate := range map[string]func([]SecondarySchema){
		"name":         func(s []SecondarySchema) { s[1].Name = s[0].Name },
		"id":           func(s []SecondarySchema) { s[1].IndexID = s[0].IndexID },
		"root":         func(s []SecondarySchema) { s[1].RootPage = s[0].RootPage },
		"cluster_name": func(s []SecondarySchema) { s[0].Name = "PRIMARY" },
		"source":       func(s []SecondarySchema) { s[0].Fields[0].Column = 0 },
		"definition":   func(s []SecondarySchema) { s[0].Fields[0].Definition.Type = "BIGINT" },
		"locator":      func(s []SecondarySchema) { s[0].ClusteredFields = []int{0} },
		"prefix":       func(s []SecondarySchema) { s[0].Fields[0].PrefixBytes = 1 },
		"space":        func(s []SecondarySchema) { s[0].SpaceID++ },
		"format":       func(s []SecondarySchema) { s[0].RowFormat = "COMPACT" },
		"wrong_root":   func(s []SecondarySchema) { s[0].RootPage = table.RootPage + 10000 },
		"wrong_id":     func(s []SecondarySchema) { s[0].IndexID += 100000 },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(indexes)
			var copy []SecondarySchema
			_ = json.Unmarshal(raw, &copy)
			mutate(copy)
			if err := ValidateSecondarySchemas(bytes.NewReader(b), size, table, copy); err == nil {
				t.Fatal("accepted wrong metadata")
			}
		})
	}
	bad := append([]byte{}, b...)
	bad[int(indexes[0].RootPage)*PageSize+200] ^= 1
	if err := ValidateSecondarySchemas(bytes.NewReader(bad), size, table, indexes); !errors.Is(err, ErrCorrupt) {
		t.Fatal("CRC", err)
	}
	if err := ValidateSecondarySchemas(bytes.NewReader(b), size, table, nil); err != nil {
		t.Fatal("empty catalog", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := QuerySecondary(ctx, bytes.NewReader(b), size, table, indexes[0], SecondaryQuery{}, ScanOptions{}, func(ProjectedRow) error { return nil })
	if !errors.Is(err, context.Canceled) || report.Complete {
		t.Fatal(err, report)
	}
}

func TestMySQL57SecondaryResourcesAndDamage(t *testing.T) {
	b, table, indexes := mysql57SecondaryFixture(t, "ordinary_dynamic")
	size := int64(len(b))
	index := indexes[0]
	reader := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: -1}
	if err := ValidateSecondarySchemas(reader, size, table, indexes); err != nil || reader.calls != len(indexes)+1 {
		t.Fatal("local preflight reads", err, reader.calls)
	}
	if err := ValidateSecondarySchemas(reader, size, table, []SecondarySchema{indexes[0], indexes[0]}); !errors.Is(err, ErrUnsupported) || reader.calls != len(indexes)+1 {
		t.Fatal("definitions must precede I/O", err)
	}
	q := SecondaryQuery{Columns: []string{"n", "id"}}
	baseline, err := QuerySecondary(t.Context(), bytes.NewReader(b), size, table, index, q, ScanOptions{}, func(ProjectedRow) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	exact := ScanOptions{MaxPageReads: baseline.PageReads, MaxRows: baseline.Records}
	if report, err := QuerySecondary(t.Context(), bytes.NewReader(b), size, table, index, q, exact, func(ProjectedRow) error { return nil }); err != nil || !report.Complete {
		t.Fatal("exact budget", err, report)
	}
	for _, options := range []ScanOptions{{MaxPageReads: baseline.PageReads - 1}, {MaxRows: 1}, {MaxEntries: 1}} {
		report, err := QuerySecondary(t.Context(), bytes.NewReader(b), size, table, index, q, options, func(ProjectedRow) error { return nil })
		if !errors.Is(err, ErrLimit) || report.Complete {
			t.Fatal("query budget", err, report)
		}
	}
	sentinel := errors.New("5.7 callback failed")
	for _, callback := range []error{sentinel, ErrStopped} {
		report, err := ScanSecondary(t.Context(), bytes.NewReader(b), size, index, ScanOptions{}, func(e SecondaryEvent) error {
			if e.Record != nil {
				return callback
			}
			return nil
		})
		if !errors.Is(err, callback) || report.Complete {
			t.Fatal("callback completion", err, report)
		}
	}
	reader = &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 2, err: sentinel}
	if err := ValidateSecondarySchemas(reader, size, table, indexes); !errors.Is(err, sentinel) {
		t.Fatal("underlying preflight I/O", err)
	}
	bad := append([]byte{}, b...)
	be.PutUint32(bad[int(index.RootPage)*PageSize+12:int(index.RootPage)*PageSize+16], table.RootPage)
	resealTestPages(bad)
	if err := ValidateSecondarySchemas(bytes.NewReader(bad), size, table, indexes); !errors.Is(err, ErrCorrupt) {
		t.Fatal("synthetic root siblings", err)
	}
	deep, _, deepIndexes := mysql57SecondaryFixture(t, "tree_dynamic")
	deepSize := int64(len(deep))
	tree, err := ReadSecondary(bytes.NewReader(deep), deepSize, deepIndexes[0])
	if err != nil {
		t.Fatal(err)
	}
	damaged := append([]byte{}, deep...)
	last := tree.Pages[len(tree.Pages)-1].Number
	damaged[int(last)*PageSize+200] ^= 1
	if err := ValidateSecondarySchemas(bytes.NewReader(damaged), deepSize, table, nil); err != nil {
		t.Fatal("empty supplied catalog", err)
	}
	// Root preflight deliberately cannot certify a corrupt descendant.
	_, deepTable, _ := mysql57SecondaryFixture(t, "tree_dynamic")
	if err := ValidateSecondarySchemas(bytes.NewReader(damaged), deepSize, deepTable, deepIndexes); err != nil {
		t.Fatal("root-local scope", err)
	}
	if result, err := ReadSecondary(bytes.NewReader(damaged), deepSize, deepIndexes[0]); !errors.Is(err, ErrCorrupt) || result != nil {
		t.Fatal("atomic damaged tree", err)
	}
	report, err := ScanSecondary(t.Context(), bytes.NewReader(damaged), deepSize, deepIndexes[0], ScanOptions{}, func(SecondaryEvent) error { return nil })
	if !errors.Is(err, ErrCorrupt) || report.Complete || report.Records == 0 {
		t.Fatal("late damage prefix", err, report)
	}
}
