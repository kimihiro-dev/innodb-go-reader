package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are independent SQL captures from the exact server patches, stored one
// level deeper so the original release-fixture matrix remains immutable.
func TestMySQL57PatchFixtures(t *testing.T) {
	totalRows, totalIndexes, totalQueries, testedVersions := 0, 0, 0, 0
	for _, version := range []string{"5.7.9", "5.7.10", "5.7.11", "5.7.12", "5.7.28", "5.7.43"} {
		t.Run(version, func(t *testing.T) {
			testedVersions++
			for _, kind := range []string{"base", "secondary"} {
				dir := filepath.Join("testdata/mysql57-patches", version, kind)
				var manifest struct {
					Status string
					Cases  []struct {
						Name, SHA256  string
						Rows, Indexes int
						Unordered     bool
					}
				}
				raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
				if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Status != "captured" || len(manifest.Cases) != 22 {
					t.Fatal("incomplete independent captures", dir, err)
				}
				for _, c := range manifest.Cases {
					t.Run(kind+"/"+c.Name, func(t *testing.T) {
						b := unzip(t, filepath.Join(dir, c.Name+".ibd.gz"))
						if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
							t.Fatal("physical SHA256 mismatch")
						}
						var schema Schema
						raw, err := os.ReadFile(filepath.Join(dir, c.Name+".json"))
						if err != nil || json.Unmarshal(raw, &schema) != nil {
							t.Fatal("schema", err)
						}
						var source struct {
							Environment struct {
								Version  string
								PageSize int `json:"page_size"`
								Checksum string
							}
						}
						raw, err = os.ReadFile(filepath.Join(dir, c.Name+".source.json"))
						if err != nil || json.Unmarshal(raw, &source) != nil || strings.SplitN(source.Environment.Version, "-", 2)[0] != version || source.Environment.PageSize != PageSize || source.Environment.Checksum != "strict_crc32" {
							t.Fatal("SQL source identity", err)
						}
						r, size := bytes.NewReader(b), int64(len(b))
						if err = ValidateSchema(r, size, schema); err != nil {
							t.Fatal(err)
						}
						if _, err = ReadAuto(r, size); !errors.Is(err, ErrUnsupported) {
							t.Fatal("5.7 has no SDI", err)
						}
						result, err := ReadMaterialized(r, size, schema)
						if err != nil {
							t.Fatal(err)
						}
						if len(schema.VirtualColumns) > 0 {
							if _, err = Read(r, size, schema); !errors.Is(err, ErrUnsupported) {
								t.Fatal("VIRTUAL complete read", err)
							}
						}
						var expected []json.RawMessage
						if err = json.Unmarshal(unzip(t, filepath.Join(dir, c.Name+".expected.json.gz")), &expected); err != nil {
							t.Fatal(err)
						}
						got := []string{}
						for _, record := range result.Result.Records {
							got = append(got, mysql57Row(t, record.Values))
							for _, external := range record.External {
								if external.Format != "BLOB" || external.HeaderOffset != 38 {
									t.Fatal("legacy BLOB identity", external)
								}
								var streamed []byte
								report, err := StreamLOB(t.Context(), r, size, external, ScanOptions{}, func(block LOBBlock) error { streamed = append(streamed, block.Data...); return nil })
								if err != nil || !report.Complete || report.Bytes != uint64(len(streamed)) {
									t.Fatal("LOB stream", err, report)
								}
								switch value := record.Values[external.Column].(type) {
								case []byte:
									if !bytes.Equal(streamed, value) {
										t.Fatal("binary LOB")
									}
								case string:
									if !bytes.Equal(streamed, record.TextBytes[external.Column]) {
										t.Fatal("text LOB")
									}
								case JSONValue:
									decoded, err := DecodeJSON(streamed)
									if err != nil {
										t.Fatal(err)
									}
									a, _ := decoded.JSON()
									b, _ := value.JSON()
									if !bytes.Equal(a, b) {
										t.Fatal("JSON LOB")
									}
								default:
									t.Fatalf("unexpected external value %T", value)
								}
							}
						}
						if len(got) != c.Rows {
							t.Fatal("row count", len(got), c.Rows)
						}
						mysql57EqualRows(t, got, expected, c.Unordered)
						totalRows += len(got)
						scanned := []string{}
						scan, err := ScanMaterialized(t.Context(), r, size, schema, ScanOptions{}, func(event ScanEvent) error {
							if event.Record != nil {
								scanned = append(scanned, mysql57Row(t, event.Record.Values))
							}
							return nil
						})
						if err != nil || !scan.Complete || scan.Records != uint64(c.Rows) {
							t.Fatal("scan", err, scan)
						}
						mysql57EqualRows(t, scanned, expected, c.Unordered)
						if !schema.hiddenRowID() {
							queried := []string{}
							report, err := QueryMaterialized(t.Context(), r, size, schema, KeyRange{}, ScanOptions{}, func(event ScanEvent) error {
								if event.Record != nil {
									queried = append(queried, mysql57Row(t, event.Record.Values))
								}
								return nil
							})
							if err != nil || !report.Complete {
								t.Fatal("clustered query", err, report)
							}
							// Query visits the same clustered order as the independent SQL ORDER BY.
							mysql57EqualRows(t, queried, expected, false)
						}
						if kind == "base" && strings.HasPrefix(c.Name, "tree_") && result.Result.Page.Level < 2 {
							t.Fatal("requires three-level tree")
						}
						if kind == "secondary" {
							indexes, queries := checkMySQL57PatchSecondary(t, dir, c.Name, r, size, schema, c.Rows, c.Unordered)
							totalIndexes += indexes
							totalQueries += queries
						}
					})
				}
			}
		})
	}
	if totalRows != testedVersions*(526+1682) || totalIndexes != testedVersions*38 || totalQueries != testedVersions*232 {
		t.Fatal("coverage", totalRows, totalIndexes, totalQueries)
	}
	t.Logf("patch SQL coverage: %d rows, %d indexes, %d queries", totalRows, totalIndexes, totalQueries)
}

func checkMySQL57PatchSecondary(t *testing.T, dir, name string, r *bytes.Reader, size int64, table Schema, rows int, unordered bool) (int, int) {
	t.Helper()
	var indexes []SecondarySchema
	raw, err := os.ReadFile(filepath.Join(dir, name+".indexes.json"))
	if err != nil || json.Unmarshal(raw, &indexes) != nil {
		t.Fatal("secondary definitions", err)
	}
	if err = ValidateSecondarySchemas(r, size, table, indexes); err != nil {
		t.Fatal(err)
	}
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
	if err = json.Unmarshal(unzip(t, filepath.Join(dir, name+".secondary.json.gz")), &sql); err != nil || len(sql) != len(indexes) {
		t.Fatal("independent secondary SQL", err)
	}
	queries := 0
	for i, index := range indexes {
		t.Run(index.Name, func(t *testing.T) {
			result, err := ReadSecondary(r, size, index)
			if err != nil || len(result.Records) != rows || sql[i].Index != index.Name {
				t.Fatal("secondary read", err)
			}
			if strings.HasPrefix(name, "tree_") && result.Page.Level < 2 {
				t.Fatal("requires three-level secondary tree")
			}
			got := []string{}
			for _, entry := range result.Records {
				values := []any{}
				for j, value := range entry.Values {
					if !index.Fields[j].RowID {
						values = append(values, value)
					}
				}
				if table.hiddenRowID() && entry.RowID == nil {
					t.Fatal("hidden locator absent")
				}
				got = append(got, mysql57Row(t, values))
			}
			mysql57EqualRows(t, got, sql[i].Entries, unordered)
			for _, q := range sql[i].Queries {
				query := SecondaryQuery{Columns: q.Columns, Range: KeyRange{Reverse: q.Reverse, Limit: q.Limit}}
				if len(q.Prefix) > 0 {
					query.Range.Prefix = mysql57SecondaryKey(t, q.Prefix, index)
				}
				if len(q.Lower) > 0 {
					query.Range.Lower = &KeyBound{Key: mysql57SecondaryKey(t, q.Lower, index)}
					query.Range.Upper = &KeyBound{Key: mysql57SecondaryKey(t, q.Upper, index), Inclusive: true}
				}
				if len(q.Point) > 0 {
					query.Range = PointKey(mysql57SecondaryKey(t, q.Point, index))
				}
				got := []string{}
				report, err := QuerySecondaryMaterialized(context.Background(), r, size, table, index, query, ScanOptions{}, func(p ProjectedRow) error { got = append(got, mysql57Row(t, p.Values)); return nil })
				if err != nil || !report.Complete {
					t.Fatal(q.Label, err, report)
				}
				mysql57EqualRows(t, got, q.Rows, unordered)
				queries++
			}
		})
	}
	return len(indexes), queries
}
