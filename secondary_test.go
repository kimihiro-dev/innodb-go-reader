package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSecondaryExisting(t *testing.T) {
	for _, c := range []struct {
		file, index string
		rows        int
	}{
		{"primary_secondary", "n_idx", 3}, {"unique_multiple", "nullable_first", 3}, {"unique_multiple", "a_later", 3}, {"unique_text_tree", "secondary_n", 600}, {"rowid_nullable_unique", "u", 3},
	} {
		t.Run(c.file+"/"+c.index, func(t *testing.T) {
			b := scanFixture(t, "testdata/cluster/"+c.file+".ibd.gz")
			result, err := ReadSecondaryAuto(bytes.NewReader(b), int64(len(b)), c.index)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != c.rows {
				t.Fatal(len(result.Records))
			}
			got := []SecondaryRecord{}
			report, err := ScanSecondaryAuto(context.Background(), bytes.NewReader(b), int64(len(b)), c.index, ScanOptions{}, func(e SecondaryEvent) error {
				if e.Record != nil {
					got = append(got, *e.Record)
				}
				return nil
			})
			if err != nil || !report.Complete || !reflect.DeepEqual(got, result.Records) {
				t.Fatal(report, err)
			}
			t.Logf("%d rows %d pages fields=%+v", len(got), len(result.Pages), result.Schema)
		})
	}
}

func secondaryJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
func secondarySQLValues(values []any) []any {
	out := append([]any{}, values...)
	for i, v := range out {
		if b, ok := v.([]byte); ok {
			out[i] = strings.ToUpper(hex.EncodeToString(b))
		}
	}
	return out
}
func TestSecondarySQL(t *testing.T) {
	var manifest struct {
		Cases []struct {
			Name          string
			Rows, Indexes int
			SHA256        string
			RowID         bool
		}
	}
	data, err := os.ReadFile("testdata/secondary/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	indexes, records, deleted, pages := 0, 0, 0, 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			base := "testdata/secondary/" + c.Name
			b := scanFixture(t, base+".ibd.gz")
			size := int64(len(b))
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA")
			}
			var expected []struct {
				Name string
				Rows [][]any
			}
			dec := json.NewDecoder(bytes.NewReader(unzip(t, base+".secondary.json.gz")))
			dec.UseNumber()
			if err := dec.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			full, err := ReadMaterializedAuto(bytes.NewReader(b), size)
			if err != nil {
				t.Fatal("clustered regression", err)
			}
			if len(full.Result.Records) != c.Rows {
				t.Fatal("clustered rows")
			}
			var whole struct{ Rows [][]any }
			dec = json.NewDecoder(bytes.NewReader(unzip(t, base+".expected.json.gz")))
			dec.UseNumber()
			if err = dec.Decode(&whole); err != nil {
				t.Fatal(err)
			}
			gotRows := []string{}
			wantRows := []string{}
			for _, r := range full.Result.Records {
				gotRows = append(gotRows, secondaryJSON(secondarySQLValues(r.Values)))
			}
			for _, r := range whole.Rows {
				wantRows = append(wantRows, secondaryJSON(r))
			}
			slices.Sort(gotRows)
			slices.Sort(wantRows)
			if !reflect.DeepEqual(gotRows, wantRows) {
				t.Fatal("full SQL")
			}
			for _, want := range expected {
				t.Run(want.Name, func(t *testing.T) {
					got, err := ReadSecondaryAuto(bytes.NewReader(b), size, want.Name)
					if err != nil {
						t.Fatal(err)
					}
					if len(got.Records) != len(want.Rows) {
						t.Fatalf("rows %d want %d", len(got.Records), len(want.Rows))
					}
					explicit, err := ReadSecondary(bytes.NewReader(b), size, got.Schema)
					if err != nil || !reflect.DeepEqual(explicit, got) {
						t.Fatal("explicit", err)
					}
					streamed := &SecondaryResult{Schema: got.Schema, Records: make([]SecondaryRecord, 0)}
					report, err := ScanSecondaryAuto(context.Background(), bytes.NewReader(b), size, want.Name, ScanOptions{}, func(e SecondaryEvent) error {
						switch {
						case e.Page != nil:
							if len(streamed.Pages) == 0 {
								streamed.Page = *e.Page
							}
							streamed.Pages = append(streamed.Pages, *e.Page)
						case e.Node != nil:
							streamed.Nodes = append(streamed.Nodes, *e.Node)
						case e.Record != nil:
							streamed.Records = append(streamed.Records, *e.Record)
						case e.DeletedRecord != nil:
							streamed.DeletedRecords = append(streamed.DeletedRecords, *e.DeletedRecord)
						}
						return nil
					})
					if err != nil || !report.Complete || !reflect.DeepEqual(streamed, got) {
						t.Fatal("scan", err)
					}
					locators := map[string]bool{}
					for _, row := range full.Result.Records {
						key := Key{}
						if row.RowID != nil {
							key = append(key, *row.RowID)
						} else {
							for _, field := range got.Schema.ClusteredFields {
								key = append(key, row.Values[got.Schema.Fields[field].Column])
							}
						}
						locators[secondaryJSON(key)] = true
					}
					for i, r := range got.Records {
						if r.DeleteMarked || !bytes.Equal(r.Raw, b[int(r.PageNumber)*PageSize+r.Start:int(r.PageNumber)*PageSize+r.End]) {
							t.Fatal("raw/mark")
						}
						actual := secondarySQLValues(r.Values)
						if c.RowID {
							actual = actual[:got.Schema.UserFields]
						}
						if secondaryJSON(actual) != secondaryJSON(want.Rows[i]) {
							t.Fatalf("row %d got %s want %s", i, secondaryJSON(actual), secondaryJSON(want.Rows[i]))
						}
						// Independently resolve the locator against the already SQL-verified cluster.
						if !locators[secondaryJSON(r.ClusteredKey)] {
							t.Fatal("unresolved locator", r.ClusteredKey)
						}
					}
					for _, r := range got.DeletedRecords {
						if !r.DeleteMarked {
							t.Fatal("deleted marker")
						}
					}
					if c.Name == "deep" && got.Page.Level < 2 {
						t.Fatal("expected three levels", got.Page.Level)
					}
					if c.Name == "changes" {
						expectedDeleted := map[string]bool{}
						for id := 0; id < 100; id++ {
							if id >= 20 && id < 90 {
								continue
							}
							var value any = id % 7
							if want.Name == "k_idx" {
								value = fmt.Sprintf("k%03d", id)
							}
							expectedDeleted[secondaryJSON([]any{value, id})] = true
						}
						for _, r := range got.DeletedRecords {
							key := secondaryJSON(r.Values)
							if !expectedDeleted[key] {
								t.Fatal("unexpected deleted key", key)
							}
							delete(expectedDeleted, key)
						}
						if len(expectedDeleted) != 0 {
							t.Fatal("missing deleted keys", expectedDeleted)
						}
					}
					indexes++
					records += len(got.Records)
					deleted += len(got.DeletedRecords)
					pages += len(got.Pages)
					t.Logf("%d rows %d deleted %d pages level %d", len(got.Records), len(got.DeletedRecords), len(got.Pages), got.Page.Level)
				})
			}
		})
	}
	t.Logf("%d indexes %d records %d deleted %d pages", indexes, records, deleted, pages)
}
