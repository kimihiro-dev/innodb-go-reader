package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
)

func projectedQuery(t testing.TB, b []byte, table Schema, index SecondarySchema, q SecondaryQuery) ([]ProjectedRow, SecondaryQueryReport, error) {
	t.Helper()
	rows := []ProjectedRow{}
	p, err := QuerySecondaryMaterialized(context.Background(), bytes.NewReader(b), int64(len(b)), table, index, q, ScanOptions{MaxEntries: 10_000_000}, func(r ProjectedRow) error { rows = append(rows, r); return nil })
	return rows, p, err
}
func TestSecondaryQueryMatrix(t *testing.T) {
	var manifest struct{ Cases []struct{ Name string } }
	raw, _ := os.ReadFile("testdata/secondary/manifest.json")
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	queries := 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := scanFixture(t, "testdata/secondary/"+c.Name+".ibd.gz")
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			table := *meta.MaterializedSchema
			cluster, err := ReadMaterialized(bytes.NewReader(b), int64(len(b)), table)
			if err != nil {
				t.Fatal(err)
			}
			for _, idx := range meta.Indexes {
				if idx.Clustered {
					continue
				}
				t.Run(idx.Name, func(t *testing.T) {
					index, err := InspectSecondary(bytes.NewReader(b), int64(len(b)), idx.Name)
					if err != nil {
						t.Fatal(err)
					}
					secondary, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), *index)
					if err != nil {
						t.Fatal(err)
					}
					lookup := map[string]Record{}
					pk, _ := table.validate()
					for _, r := range cluster.Result.Records {
						var k Key
						if r.RowID != nil {
							k = Key{*r.RowID}
						} else {
							for _, i := range pk {
								k = append(k, r.Values[i])
							}
						}
						lookup[secondaryJSON(k)] = r
					}
					ordered := []Record{}
					for _, r := range secondary.Records {
						ordered = append(ordered, lookup[secondaryJSON(r.ClusteredKey)])
					}
					cases := []KeyRange{{}, {Reverse: true}, {Limit: 2}}
					if len(ordered) > 0 {
						key := func(r Record) Key {
							v := Key{}
							for _, f := range index.Fields[:index.UserFields] {
								v = append(v, r.Values[f.Column])
							}
							return v
						}
						a, z := key(ordered[len(ordered)/3]), key(ordered[2*len(ordered)/3])
						cases = append(cases, PointKey(a), KeyRange{Lower: &KeyBound{Key: a, Inclusive: true}, Upper: &KeyBound{Key: z, Inclusive: true}}, KeyRange{Lower: &KeyBound{Key: a}, Upper: &KeyBound{Key: z}, Reverse: true, Limit: 3})
						for n := 1; n <= len(a); n++ {
							cases = append(cases, KeyRange{Prefix: a[:n]}, KeyRange{Prefix: a[:n], Reverse: true, Limit: 2})
						}
					}
					col := index.Fields[index.ClusteredFields[0]].Column
					if col < 0 {
						col = 0
					}
					projections := [][]string{nil, {table.Columns[col].Name}}
					// Hidden ROW_ID is not a projection column; test the first stored column instead.
					if table.hiddenRowID() {
						projections[1] = []string{table.Columns[0].Name}
					}
					for _, q := range cases {
						for _, projection := range projections {
							selection, err := prepareSecondaryRange(q, *index)
							if err != nil {
								t.Fatal(err)
							}
							expected := [][]any{}
							if !selection.empty {
								for _, r := range ordered {
									key := Key{}
									for _, f := range index.Fields[:index.UserFields] {
										key = append(key, r.Values[f.Column])
									}
									encoded, err := encodeSecondaryKey(key, index.Fields[:index.UserFields])
									if err != nil {
										t.Fatal(err)
									}
									if !selection.matches(encoded) {
										continue
									}
									values := r.Values
									if projection != nil {
										values = nil
										for _, name := range projection {
											for i, c := range table.Columns {
												if c.Name == name {
													values = append(values, r.Values[i])
												}
											}
										}
									}
									expected = append(expected, values)
								}
							}
							if q.Reverse {
								slices.Reverse(expected)
							}
							if q.Limit > 0 && uint64(len(expected)) > q.Limit {
								expected = expected[:q.Limit]
							}
							got, p, err := projectedQuery(t, b, table, *index, SecondaryQuery{Range: q, Columns: projection})
							if err != nil || !p.Complete {
								t.Fatal(q, err)
							}
							actual := [][]any{}
							for _, r := range got {
								actual = append(actual, r.Values)
							}
							if !reflect.DeepEqual(actual, expected) {
								t.Fatalf("mismatch range %+v projection %v: got %d expected %d", q, projection, len(actual), len(expected))
							}
							if p.LimitReached != (q.Limit > 0 && uint64(len(expected)) == q.Limit) {
								t.Fatal("limit", p)
							}
							queries++
						}
					}
				})
			}
		})
	}
	t.Log("queries", queries)
}

func TestSecondaryQuerySQL(t *testing.T) {
	var manifest struct {
		Cases []struct {
			Name, SHA256     string
			Queries          int
			FullReadRejected bool `json:"full_read_rejected"`
		}
	}
	raw, err := os.ReadFile("testdata/secondary_query/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	queries, rows := 0, 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			base := "testdata/secondary_query/" + c.Name
			b := scanFixture(t, base+".ibd.gz")
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA")
			}
			size := int64(len(b))
			meta, err := InspectTable(bytes.NewReader(b), size)
			if err != nil {
				t.Fatal(err)
			}
			s, err := InspectSecondary(bytes.NewReader(b), size, "s")
			if err != nil {
				t.Fatal(err)
			}
			if !c.FullReadRejected {
				var expected struct{ Rows [][]any }
				dec := json.NewDecoder(bytes.NewReader(unzip(t, base+".expected.json.gz")))
				dec.UseNumber()
				if err = dec.Decode(&expected); err != nil {
					t.Fatal(err)
				}
				full, err := ReadAuto(bytes.NewReader(b), size)
				if err != nil {
					t.Fatal(err)
				}
				actual := [][]any{}
				for _, r := range full.Records {
					actual = append(actual, r.Values)
				}
				if secondaryJSON(actual) != secondaryJSON(expected.Rows) {
					t.Fatal("full SQL")
				}
			} else {
				if _, err := ReadAuto(bytes.NewReader(b), size); !errors.Is(err, ErrUnsupported) {
					t.Fatal("oversized full read", err)
				}
			}
			var specs []struct {
				Query SecondaryQuery
				Rows  [][]any
			}
			dec := json.NewDecoder(bytes.NewReader(unzip(t, base+".queries.json.gz")))
			dec.UseNumber()
			if err = dec.Decode(&specs); err != nil {
				t.Fatal(err)
			}
			for i, spec := range specs {
				got, p, err := projectedQuery(t, b, *meta.MaterializedSchema, *s, spec.Query)
				if err != nil || !p.Complete {
					t.Fatalf("case %d: %v", i, err)
				}
				actual := [][]any{}
				for _, r := range got {
					actual = append(actual, r.Values)
				}
				if secondaryJSON(actual) != secondaryJSON(spec.Rows) {
					t.Fatalf("case %d got %d want %d", i, len(actual), len(spec.Rows))
				}
				auto := []ProjectedRow{}
				report, err := QuerySecondaryAuto(context.Background(), bytes.NewReader(b), size, "s", spec.Query, ScanOptions{MaxEntries: 10_000_000}, func(r ProjectedRow) error { auto = append(auto, r); return nil })
				if err != nil || !report.Complete || !reflect.DeepEqual(auto, got) {
					t.Fatalf("auto %d: %v", i, err)
				}
				if p.Records != uint64(len(got)) || p.Covered+p.Lookups < p.Records {
					t.Fatal("counts", p)
				}
				queries++
				rows += len(got)
			}
		})
	}
	t.Logf("%d SQL queries, %d projected rows", queries, rows)
}
