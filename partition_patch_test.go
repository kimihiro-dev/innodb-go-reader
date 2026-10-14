package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type patchPartitionCase struct {
	Name, Table                   string
	Rows                          int
	Supported, Unordered, Virtual bool
	Files                         []struct {
		Partition, File, SHA256 string
		Rows                    int
	}
}

func patchPartitionInputs(t testing.TB, dir string, c patchPartitionCase) []PartitionInput {
	t.Helper()
	var inputs []PartitionInput
	for _, f := range c.Files {
		b := unzip(t, filepath.Join(dir, f.File))
		if fmt.Sprintf("%x", sha256.Sum256(b)) != f.SHA256 {
			t.Fatal("partition SHA256", f.File)
		}
		inputs = append(inputs, PartitionInput{Name: f.Partition, Reader: bytes.NewReader(b), Size: int64(len(b))})
	}
	return inputs
}

func TestPartitionPatchFixtures(t *testing.T) {
	manifests, err := filepath.Glob("testdata/partition-patches/*/manifest.json")
	if err != nil || len(manifests) != 15 {
		t.Fatal("missing partition captures", err)
	}
	files, rows, cases := 0, 0, 0
	for _, path := range manifests {
		dir := filepath.Dir(path)
		var m struct {
			Status      string
			Environment struct {
				Version  string
				PageSize int `json:"page_size"`
			}
			Cases []patchPartitionCase
		}
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &m) != nil || m.Status != "captured" || strings.SplitN(m.Environment.Version, "-", 2)[0] != filepath.Base(dir) || m.Environment.PageSize != PageSize {
			t.Fatal("capture identity", err, path)
		}
		for _, c := range m.Cases {
			t.Run(filepath.Base(dir)+"/"+c.Name, func(t *testing.T) {
				inputs := patchPartitionInputs(t, dir, c)
				var definition *PartitionSchema
				if strings.HasPrefix(m.Environment.Version, "5.") {
					if !c.Supported {
						return
					} // SQL collector rejection is verified separately.
					var schema PartitionSchema
					raw, err := os.ReadFile(filepath.Join(dir, c.Name+".partitions.json"))
					if err != nil || json.Unmarshal(raw, &schema) != nil {
						t.Fatal("trusted definition", err)
					}
					definition = &schema
				}
				var set *PartitionSet
				if definition != nil {
					set, err = InspectPartitionsWithSchema(t.Context(), inputs, *definition, ScanOptions{})
				} else {
					set, err = InspectPartitions(t.Context(), inputs, ScanOptions{})
				}
				if !c.Supported {
					if set != nil || err == nil {
						t.Fatal("unsupported", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if set.Name != c.Table || len(set.Partitions) != len(inputs) {
					t.Fatal("logical identity")
				}
				var expected struct {
					Rows       []json.RawMessage
					Partitions map[string][]json.RawMessage
				}
				if err := json.Unmarshal(unzip(t, filepath.Join(dir, c.Name+".expected.json.gz")), &expected); err != nil {
					t.Fatal(err)
				}
				got := map[string][]string{}
				for _, f := range c.Files {
					got[f.Partition] = []string{}
				}
				metadata, order := false, uint32(0)
				yield := func(e PartitionEvent) error {
					if e.Metadata != nil {
						if metadata {
							t.Fatal("duplicate metadata")
						}
						metadata = true
						return nil
					}
					if !metadata || e.Source == nil || e.Source.Number < order {
						t.Fatal("partition source/order")
					}
					order = e.Source.Number
					if e.Event.Record != nil {
						got[e.Source.Name] = append(got[e.Source.Name], mysql57Row(t, e.Event.Record.Values))
					}
					return nil
				}
				var report PartitionScanReport
				if definition != nil {
					report, err = ScanPartitionsMaterializedWithSchema(t.Context(), inputs, *definition, ScanOptions{}, yield)
				} else {
					report, err = ScanPartitionsMaterialized(t.Context(), inputs, ScanOptions{}, yield)
				}
				if err != nil || !report.Complete || report.CompletedPartitions != uint64(len(inputs)) || report.Records != uint64(c.Rows) {
					t.Fatal("partition scan", err, report)
				}
				for name, want := range expected.Partitions {
					mysql57EqualRows(t, got[name], want, c.Unordered)
				}
				for _, part := range set.Partitions {
					var input PartitionInput
					for _, in := range inputs {
						if in.Name == part.Source.Name {
							input = in
						}
					}
					indexes := part.Indexes
					if definition == nil {
						for _, entry := range part.Table.Indexes {
							if entry.Clustered {
								continue
							}
							index, err := inspectSecondaryMetadata(part.Table, entry.Name)
							if err != nil {
								t.Fatal(err)
							}
							indexes = append(indexes, *index)
						}
					}
					for _, index := range indexes {
						actual := []string{}
						report, err := QuerySecondaryMaterialized(t.Context(), input.Reader, input.Size, *part.Table.MaterializedSchema, index, SecondaryQuery{}, ScanOptions{}, func(row ProjectedRow) error { actual = append(actual, mysql57Row(t, row.Values)); return nil })
						if err != nil || !report.Complete {
							t.Fatal("partition secondary lookup", index.Name, err)
						}
						mysql57EqualRows(t, actual, expected.Partitions[part.Source.Name], true)
					}
				}
				if report.PageReads != report.CacheHits+report.PhysicalReads {
					t.Fatal("read accounting")
				}
				files += len(inputs)
				rows += c.Rows
				cases++
			})
		}
	}
	if cases != 289 || files != 867 || rows != 12604 {
		t.Fatalf("partition patch SQL coverage: %d collections, %d files, %d rows", cases, files, rows)
	}
	t.Logf("partition patch SQL coverage: %d collections, %d files, %d rows", cases, files, rows)
}
