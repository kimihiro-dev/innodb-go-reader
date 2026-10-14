package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func trustedPartitionFixture(t testing.TB) ([]PartitionInput, PartitionSchema) {
	t.Helper()
	dir := "testdata/partition-patches/5.7.28"
	var manifest struct{ Cases []patchPartitionCase }
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	var c patchPartitionCase
	for _, entry := range manifest.Cases {
		if entry.Name == "ranges_dynamic_current" {
			c = entry
		}
	}
	var definition PartitionSchema
	raw, err = os.ReadFile(filepath.Join(dir, c.Name+".partitions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	return patchPartitionInputs(t, dir, c), definition
}

func copyTrustedPartition(t testing.TB, s PartitionSchema) PartitionSchema {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var result PartitionSchema
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTrustedPartitionDefinitionFailures(t *testing.T) {
	inputs, s := trustedPartitionFixture(t)
	changes := map[string]func(*PartitionSchema){
		"version":       func(s *PartitionSchema) { s.Version = 2 },
		"mysql-version": func(s *PartitionSchema) { s.MySQLVersion = "5.7.42" },
		"database":      func(s *PartitionSchema) { s.Database = "" },
		"method":        func(s *PartitionSchema) { s.PartitionType = 2 },
		"order":         func(s *PartitionSchema) { s.Partitions[0].Number = 1 },
		"name":          func(s *PartitionSchema) { s.Partitions[1].Name = s.Partitions[0].Name },
		"table-id":      func(s *PartitionSchema) { s.Partitions[1].TableID = s.Partitions[0].TableID },
		"space-id":      func(s *PartitionSchema) { s.Partitions[1].Schema.SpaceID = s.Partitions[0].Schema.SpaceID },
		"index-id":      func(s *PartitionSchema) { s.Partitions[1].Schema.IndexID = s.Partitions[0].Schema.IndexID },
		"columns":       func(s *PartitionSchema) { s.Partitions[1].Schema.Columns[2].MaxChars++ },
		"indexes":       func(s *PartitionSchema) { s.Partitions[1].Indexes = nil },
		"descending":    func(s *PartitionSchema) { s.Partitions[0].Indexes[0].Fields[0].Definition.Descending = true },
		"instant":       func(s *PartitionSchema) { s.Partitions[0].Schema.Instant = &InstantLayout{} },
		"missing":       func(s *PartitionSchema) { s.Partitions = s.Partitions[:2] },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			bad := copyTrustedPartition(t, s)
			change(&bad)
			calls := 0
			r, err := ScanPartitionsWithSchema(t.Context(), inputs, bad, ScanOptions{}, func(PartitionEvent) error { calls++; return nil })
			if err == nil || r.Complete || calls != 0 {
				t.Fatal("invalid definition emitted events", err, r)
			}
		})
	}
}

func TestTrustedPartitionPhysicalFailures(t *testing.T) {
	inputs, s := trustedPartitionFixture(t)
	for _, kind := range []string{"missing", "extra", "duplicate", "wrong-name", "swapped-file", "checksum", "wrong-root", "wrong-secondary-root"} {
		t.Run(kind, func(t *testing.T) {
			in := append([]PartitionInput(nil), inputs...)
			definition := copyTrustedPartition(t, s)
			switch kind {
			case "missing":
				in = in[:2]
			case "extra":
				in = append(in, PartitionInput{Name: "extra", Reader: in[0].Reader, Size: in[0].Size})
			case "duplicate":
				in[1] = in[0]
			case "wrong-name":
				in[0].Name = "elsewhere"
			case "swapped-file":
				in[0].Reader = in[1].Reader
				in[0].Size = in[1].Size
			case "checksum":
				b := make([]byte, in[0].Size)
				if _, err := in[0].Reader.ReadAt(b, 0); err != nil {
					t.Fatal(err)
				}
				b[54] ^= 1
				in[0].Reader = bytes.NewReader(b)
			case "wrong-root":
				definition.Partitions[0].Schema.RootPage++
			case "wrong-secondary-root":
				definition.Partitions[0].Indexes[0].RootPage = definition.Partitions[0].Schema.RootPage
			}
			calls := 0
			r, err := ScanPartitionsWithSchema(t.Context(), in, definition, ScanOptions{}, func(PartitionEvent) error { calls++; return nil })
			if err == nil || r.Complete || calls != 0 {
				t.Fatal("invalid collection emitted events", err, r)
			}
			if set, err := InspectPartitionsWithSchema(t.Context(), in, definition, ScanOptions{}); err == nil || set != nil {
				t.Fatal("partial metadata")
			}
		})
	}
}

func TestTrustedPartitionControlAndOwnership(t *testing.T) {
	inputs, s := trustedPartitionFixture(t)
	for _, options := range []ScanOptions{{MaxPageReads: 1}, {MaxEntries: 1}} {
		calls := 0
		r, err := ScanPartitionsWithSchema(t.Context(), inputs, s, options, func(PartitionEvent) error { calls++; return nil })
		if !errors.Is(err, ErrLimit) || r.Complete || calls != 0 {
			t.Fatal("preflight limit", err, r)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if set, err := InspectPartitionsWithSchema(ctx, inputs, s, ScanOptions{}); set != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r, err := ScanPartitionsWithSchema(t.Context(), inputs, s, ScanOptions{MaxRows: 1}, func(PartitionEvent) error { return nil })
	if !errors.Is(err, ErrLimit) || r.Complete || r.Records != 1 {
		t.Fatal("cumulative row limit", err, r)
	}
	r, err = ScanPartitionsWithSchema(t.Context(), inputs, s, ScanOptions{}, func(PartitionEvent) error { return ErrStopped })
	if !errors.Is(err, ErrStopped) || r.Complete {
		t.Fatal("callback failure", err)
	}
	before := copyTrustedPartition(t, s)
	r, err = ScanPartitionsWithSchema(t.Context(), inputs, s, ScanOptions{}, func(e PartitionEvent) error {
		if e.Metadata != nil {
			for _, p := range e.Metadata.Partitions {
				p.Table.MaterializedSchema.RootPage = 9999
				p.Table.MaterializedSchema.Columns[0].Type = "BAD"
				p.Indexes[0].Name = "changed"
			}
			return nil
		}
		if e.Source.Name != inputs[e.Source.Number].Name {
			t.Fatal("redirected provenance")
		}
		return nil
	})
	if err != nil || !r.Complete || r.Records != 23 {
		t.Fatal("callback redirected scan", err, r)
	}
	a, _ := json.Marshal(s)
	b, _ := json.Marshal(before)
	if !bytes.Equal(a, b) {
		t.Fatal("mutated caller definition")
	}
}

func TestLegacyPartitionMetadataFailures(t *testing.T) {
	dir := "testdata/partition-patches/8.0.28"
	var manifest struct{ Cases []patchPartitionCase }
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	var c patchPartitionCase
	for _, entry := range manifest.Cases {
		if entry.Name == "instant_dynamic_add" {
			c = entry
		}
	}
	inputs := patchPartitionInputs(t, dir, c)
	b := unzip(t, filepath.Join(dir, c.Files[0].File))
	for name, change := range map[string]func(map[string]any){
		"below-logical-minimum": func(o map[string]any) {
			o["dd_object"].(map[string]any)["partitions"].([]any)[1].(map[string]any)["se_private_data"] = "instant_col=1;"
		},
		"excessive-count": func(o map[string]any) {
			o["dd_object"].(map[string]any)["partitions"].([]any)[1].(map[string]any)["se_private_data"] = "instant_col=999;"
		},
		"nondecimal-count": func(o map[string]any) {
			o["dd_object"].(map[string]any)["partitions"].([]any)[1].(map[string]any)["se_private_data"] = "instant_col=x;"
		},
		"duplicate-property": func(o map[string]any) {
			o["dd_object"].(map[string]any)["partitions"].([]any)[1].(map[string]any)["se_private_data"] = "instant_col=3;instant_col=3;"
		},
		"default-on-original-column": func(o map[string]any) {
			column := o["dd_object"].(map[string]any)["columns"].([]any)[0].(map[string]any)
			column["se_private_data"] = column["se_private_data"].(string) + "default=80000001;"
		},
		"tuple-mismatch": func(o map[string]any) { o["dd_version"] = 80017 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := partitionSDIMutation(t, b, change)
			in := append([]PartitionInput(nil), inputs...)
			in[0].Reader = bytes.NewReader(bad)
			calls := 0
			r, err := ScanPartitions(t.Context(), in, ScanOptions{}, func(PartitionEvent) error { calls++; return nil })
			if err == nil || r.Complete || calls != 0 {
				t.Fatal("invalid legacy partition emitted", err, r)
			}
		})
	}
}
