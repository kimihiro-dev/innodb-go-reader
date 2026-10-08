package innodb

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type partitionFixtureCase struct {
	Name, Table string
	Supported   bool
	Rows        int
	Files       []struct {
		Partition, File string
		Rows            int
	}
}

func partitionFixtures(t *testing.T) []partitionFixtureCase {
	t.Helper()
	b, err := os.ReadFile("testdata/partitions/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Status string
		Cases  []partitionFixtureCase
	}
	if err = json.Unmarshal(b, &m); err != nil || m.Status != "captured" {
		t.Fatal("fixture manifest", err)
	}
	return m.Cases
}
func partitionBytes(t testing.TB, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata/partitions", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func partitionInputs(t *testing.T, c partitionFixtureCase) []PartitionInput {
	t.Helper()
	var in []PartitionInput
	for _, f := range c.Files {
		b := partitionBytes(t, f.File)
		in = append(in, PartitionInput{Name: f.Partition, Reader: bytes.NewReader(b), Size: int64(len(b))})
	}
	return in
}
func TestPartitionFixtures(t *testing.T) {
	success, total := 0, 0
	for _, c := range partitionFixtures(t) {
		t.Run(c.Name, func(t *testing.T) {
			in := partitionInputs(t, c)
			sort.Slice(in, func(i, j int) bool { return in[i].Name > in[j].Name })
			set, err := InspectPartitions(context.Background(), in, ScanOptions{})
			if !c.Supported {
				if !errors.Is(err, ErrUnsupported) || set != nil {
					t.Fatal("unsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if set.Name != c.Table || len(set.Partitions) != len(in) {
				t.Fatal("metadata")
			}
			// SQL expectations are independent, not regenerated from Go reports.
			var want struct {
				Rows       [][]any
				Partitions map[string][][]any
			}
			dec := json.NewDecoder(bytes.NewReader(partitionBytes(t, c.Name+".expected.json.gz")))
			dec.UseNumber()
			if err = dec.Decode(&want); err != nil {
				t.Fatal(err)
			}
			got := map[string][][]any{}
			names := []string{}
			first := true
			report, err := ScanPartitionsMaterialized(context.Background(), in, ScanOptions{}, func(e PartitionEvent) error {
				if first {
					first = false
					if e.Metadata == nil || e.Source != nil {
						t.Fatal("metadata must be first")
					}
					return nil
				}
				if e.Metadata != nil || e.Source == nil {
					t.Fatal("event source")
				}
				if e.Event.Record != nil {
					b, err := json.Marshal(e.Event.Record.Values)
					if err != nil {
						return err
					}
					var row []any
					d := json.NewDecoder(bytes.NewReader(b))
					d.UseNumber()
					if err = d.Decode(&row); err != nil {
						return err
					}
					got[e.Source.Name] = append(got[e.Source.Name], row)
					names = append(names, e.Source.Name)
				}
				return nil
			})
			if err != nil || !report.Complete || report.CompletedPartitions != uint64(len(in)) || int(report.Records) != c.Rows {
				t.Fatal(report, err)
			}
			if !sort.StringsAreSorted(names) {
				t.Fatal("partition order")
			}
			for name, rows := range want.Partitions {
				if len(rows) == 0 && len(got[name]) == 0 {
					continue
				}
				if !reflect.DeepEqual(rows, got[name]) {
					t.Fatalf("SQL values %s", name)
				}
			}
			if report.PageReads != report.CacheHits+report.PhysicalReads {
				t.Fatal("counters")
			}
			strict, err := ScanPartitions(context.Background(), in, ScanOptions{}, func(PartitionEvent) error { return nil })
			if len(set.VirtualColumns) > 0 {
				if !errors.Is(err, ErrUnsupported) || strict.Complete {
					t.Fatal("strict VIRTUAL")
				}
			} else if err != nil || !strict.Complete {
				t.Fatal(err)
			}
			for _, input := range in {
				if r, err := ReadAuto(input.Reader, input.Size); err == nil || r != nil {
					t.Fatal("single file accepted as full table")
				}
			}
			success++
			total += c.Rows
		})
	}
	if success != 11 || total != 3184 {
		t.Fatalf("success=%d rows=%d", success, total)
	}
	t.Logf("%d collections %d SQL rows", success, total)
}
func TestPartitionPreflightFailures(t *testing.T) {
	cases := partitionFixtures(t)
	byName := map[string][]PartitionInput{}
	for _, c := range cases {
		byName[c.Name] = partitionInputs(t, c)
	}
	base := byName["ranges"]
	bad := [][]PartitionInput{nil, base[:2], base[1:], append(append([]PartitionInput{}, base...), base[0]), {base[0], byName["lists"][1], base[2]}, {byName["range_rebuilt_nonanchor"][0], byName["range_exchanged"][1], byName["range_rebuilt_nonanchor"][2]}}
	duplicate := append([]PartitionInput{}, base...)
	duplicate[1] = PartitionInput{Name: base[1].Name, Reader: base[0].Reader, Size: base[0].Size}
	bad = append(bad, duplicate)
	swapped := append([]PartitionInput{}, base...)
	swapped[0].Name, swapped[1].Name = swapped[1].Name, swapped[0].Name
	bad = append(bad, swapped)
	for i, in := range bad {
		calls := 0
		report, err := ScanPartitions(context.Background(), in, ScanOptions{}, func(PartitionEvent) error { calls++; return nil })
		if err == nil || report.Complete || calls != 0 {
			t.Fatalf("case%d emitted/accepted: %v", i, err)
		}
		if m, err := InspectPartitions(context.Background(), in, ScanOptions{}); err == nil || m != nil {
			t.Fatal("metadata partial")
		}
	}
}
func TestPartitionControl(t *testing.T) {
	var in []PartitionInput
	for _, c := range partitionFixtures(t) {
		if c.Name == "ranges" {
			in = partitionInputs(t, c)
		}
	}
	for _, o := range []ScanOptions{{MaxPageReads: 1}, {MaxEntries: 1}, {MaxRows: 501}} {
		report, err := ScanPartitions(context.Background(), in, o, func(PartitionEvent) error { return nil })
		if !errors.Is(err, ErrLimit) || report.Complete {
			t.Fatal(report, err)
		}
		if o.MaxRows == 501 && report.Records != 501 {
			t.Fatal("row budget reset across files")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := ScanPartitions(ctx, in, ScanOptions{}, func(PartitionEvent) error { return nil }); !errors.Is(err, context.Canceled) || r.Complete {
		t.Fatal(err)
	}
	for _, stopAfter := range []int{0, 2, 510} {
		n := 0
		r, err := ScanPartitions(context.Background(), in, ScanOptions{}, func(e PartitionEvent) error {
			if n == stopAfter {
				return ErrStopped
			}
			if e.Event.Record != nil {
				n++
			}
			return nil
		})
		if !errors.Is(err, ErrStopped) || r.Complete {
			t.Fatal(err)
		}
	}
	// Mutating delivered metadata must not change later traversal or provenance.
	r, err := ScanPartitions(context.Background(), in, ScanOptions{}, func(e PartitionEvent) error {
		if e.Metadata != nil {
			e.Metadata.Columns[0].Name = "changed"
			for i := range e.Metadata.Partitions {
				e.Metadata.Partitions[i].Source.TableID = 0
				e.Metadata.Partitions[i].Table.MaterializedSchema.RootPage = 999999
			}
		} else if e.Source.TableID == 0 {
			t.Fatal("source alias")
		}
		return nil
	})
	if err != nil || !r.Complete || r.Columns[0].Name == "changed" {
		t.Fatal("metadata ownership", err)
	}
}
