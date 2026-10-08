package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Rebuild only the SDI leaf in a memory copy. Values and index pages retain
// their captured bytes, so malformed metadata must fail the real preflight.
func partitionSDIMutation(t testing.TB, source []byte, change func(map[string]any)) []byte {
	t.Helper()
	b := append([]byte(nil), source...)
	sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(sdi.Pages) != 1 || sdi.Pages[0].Level != 0 {
		t.Fatal("expected one SDI leaf")
	}
	p := b[int(sdi.Pages[0].Number)*PageSize : int(sdi.Pages[0].Number+1)*PageSize]
	clear(p[120 : PageSize-8])
	pos := 120
	origins := []int{}
	for _, rec := range sdi.Records {
		raw := rec.JSON
		if rec.Key.Type == 1 {
			var obj map[string]any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err = d.Decode(&obj); err != nil {
				t.Fatal(err)
			}
			change(obj)
			raw, err = json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
		}
		payload := compressSDITest(t, raw)
		n := len(payload)
		if n < 128 || n > 16383 || pos+40+n >= PageSize-16 {
			t.Fatal("test SDI capacity")
		}
		o := pos + 7
		origins = append(origins, o)
		p[o-6] = byte(n>>8) | 0x80
		p[o-7] = byte(n)
		be.PutUint16(p[o-4:], uint16(len(origins)+1)<<3)
		be.PutUint32(p[o:], rec.Key.Type)
		be.PutUint64(p[o+4:], rec.Key.ID)
		be.PutUint32(p[o+25:], uint32(len(raw)))
		be.PutUint32(p[o+29:], uint32(n))
		copy(p[o+33:], payload)
		pos = o + 33 + n
	}
	for i, o := range origins {
		next := supremum
		if i+1 < len(origins) {
			next = origins[i+1]
		}
		be.PutUint16(p[o-2:], uint16((next-o)&(PageSize-1)))
	}
	be.PutUint16(p[97:], uint16(origins[0]-99))
	p[107] = byte(len(origins) + 1)
	be.PutUint16(p[38:], 2)
	be.PutUint16(p[40:], uint16(pos))
	be.PutUint16(p[42:], 0x8000|uint16(len(origins)+2))
	be.PutUint16(p[44:], 0)
	be.PutUint16(p[46:], 0)
	be.PutUint16(p[54:], uint16(len(origins)))
	be.PutUint16(p[PageSize-10:], infimum)
	be.PutUint16(p[PageSize-12:], supremum)
	resealTestPages(b)
	return b
}
func TestPartitionMetadataDamage(t *testing.T) {
	in := partitionInputs(t, partitionFixtures(t)[1])
	base := partitionBytes(t, "ranges-p0.partition.gz")
	part := func(o map[string]any) map[string]any {
		return o["dd_object"].(map[string]any)["partitions"].([]any)[0].(map[string]any)
	}
	table := func(o map[string]any) map[string]any { return o["dd_object"].(map[string]any) }
	index := func(o map[string]any) map[string]any { return part(o)["indexes"].([]any)[0].(map[string]any) }
	changes := map[string]func(map[string]any){
		"version":            func(o map[string]any) { o["mysqld_version_id"] = 80044 },
		"object":             func(o map[string]any) { o["dd_object_type"] = "Other" },
		"subpartition":       func(o map[string]any) { table(o)["subpartition_type"] = 1 },
		"method":             func(o map[string]any) { table(o)["partition_type"] = 2 },
		"logical-id":         func(o map[string]any) { table(o)["se_private_id"] = 1 },
		"engine":             func(o map[string]any) { table(o)["engine"] = "Other" },
		"missing-directory":  func(o map[string]any) { table(o)["partitions"] = []any{} },
		"no-index":           func(o map[string]any) { table(o)["indexes"] = []any{} },
		"expression":         func(o map[string]any) { table(o)["partition_expression"] = 42 },
		"name":               func(o map[string]any) { part(o)["name"] = "absent" },
		"missing-number":     func(o map[string]any) { delete(part(o), "number") },
		"position":           func(o map[string]any) { part(o)["number"] = 3 },
		"duplicate-position": func(o map[string]any) { part(o)["number"] = 1 },
		"parent":             func(o map[string]any) { part(o)["parent_partition_id"] = 0 },
		"partition-id":       func(o map[string]any) { part(o)["se_private_id"] = 0 },
		"nested":             func(o map[string]any) { part(o)["subpartitions"] = []any{map[string]any{}} },
		"index-count":        func(o map[string]any) { part(o)["indexes"] = []any{} },
		"index-opx":          func(o map[string]any) { index(o)["index_opx"] = 9 },
		"duplicate-opx":      func(o map[string]any) { index(o)["index_opx"] = 1 },
		"space-ref":          func(o map[string]any) { index(o)["tablespace_ref"] = "wrong" },
		"physical-table": func(o map[string]any) {
			index(o)["se_private_data"] = strings.ReplaceAll(index(o)["se_private_data"].(string), "table_id=1883", "table_id=1")
		},
		"logical-physical": func(o map[string]any) { table(o)["indexes"].([]any)[0].(map[string]any)["se_private_data"] = "id=1;" },
		"private-unknown":  func(o map[string]any) { part(o)["se_private_data"] = "unknown=1;" },
		"private-conflict": func(o map[string]any) {
			part(o)["se_private_data"] = "autoinc=1;"
			table(o)["se_private_data"] = "autoinc=2;"
		},
		"column-owner": func(o map[string]any) {
			table(o)["columns"].([]any)[0].(map[string]any)["se_private_data"] = "table_id=1;"
		},
		"column-layout": func(o map[string]any) { table(o)["columns"].([]any)[0].(map[string]any)["ordinal_position"] = 99 },
	}
	changes["logical-tablespace"] = func(o map[string]any) { table(o)["indexes"].([]any)[0].(map[string]any)["tablespace_ref"] = "wrong" }
	changes["logical-private-null"] = func(o map[string]any) { table(o)["indexes"].([]any)[0].(map[string]any)["se_private_data"] = nil }
	// Verify the synthetic leaf itself before testing rejected changes.
	valid := partitionSDIMutation(t, base, func(map[string]any) {})
	in[0].Reader = bytes.NewReader(valid)
	if _, err := InspectPartitions(t.Context(), in, ScanOptions{}); err != nil {
		t.Fatal("mutation helper", err)
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			b := partitionSDIMutation(t, base, change)
			in[0].Reader = bytes.NewReader(b)
			calls := 0
			r, err := ScanPartitions(t.Context(), in, ScanOptions{}, func(PartitionEvent) error { calls++; return nil })
			if err == nil || r.Complete || calls != 0 {
				t.Fatal("accepted/emitted malformed metadata", err)
			}
			if m, err := InspectPartitions(t.Context(), in, ScanOptions{}); err == nil || m != nil {
				t.Fatal("partial metadata", err)
			}
		})
	}
}
func TestPartitionOfficialSDI(t *testing.T) {
	total := 0
	for _, c := range partitionFixtures(t) {
		for _, f := range c.Files {
			b := partitionBytes(t, f.File)
			got, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(partitionBytes(t, strings.TrimSuffix(f.File, ".partition.gz")+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(got.Records)+1 {
				t.Fatal("object count")
			}
			decode := func(b []byte) any {
				var v any
				d := json.NewDecoder(bytes.NewReader(b))
				d.UseNumber()
				if err := d.Decode(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			for i, r := range got.Records {
				var want struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &want); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{want.Type, want.ID}) || !reflect.DeepEqual(decode(r.JSON), decode(want.Object)) {
					t.Fatal(f.File, "official mismatch")
				}
				total++
			}
		}
	}
	if total != 46 {
		t.Fatal("total objects", total)
	}
}
func TestPartitionLateFailureAndBudgets(t *testing.T) {
	in := partitionInputs(t, partitionFixtures(t)[1])
	var page uint32
	var saved []*PartitionSource
	full, err := ScanPartitions(t.Context(), in, ScanOptions{}, func(e PartitionEvent) error {
		if e.Event.Record != nil {
			saved = append(saved, e.Source)
			if e.Source.Name == "p1" {
				page = e.Event.Record.PageNumber
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].Name != "p0" || saved[len(saved)-1].Name != "p1" {
		t.Fatal("retained source overwritten")
	}
	for _, o := range []ScanOptions{{MaxPageReads: full.PageReads - 1}, {MaxEntries: full.TraversalEntries - 1}, {MaxRows: full.Records - 1}} {
		r, err := ScanPartitions(t.Context(), in, o, func(PartitionEvent) error { return nil })
		if !errors.Is(err, ErrLimit) || r.Complete {
			t.Fatal("aggregate limit", err)
		}
	}
	r, err := ScanPartitions(t.Context(), in, ScanOptions{MaxPageReads: full.PageReads, MaxEntries: full.TraversalEntries, MaxRows: full.Records}, func(PartitionEvent) error { return nil })
	if err != nil || !r.Complete {
		t.Fatal("exact budget", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, err = ScanPartitions(ctx, in, ScanOptions{}, func(e PartitionEvent) error {
		if e.Event.Record != nil && e.Source.Name == "p1" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || r.Complete || r.CompletedPartitions != 1 {
		t.Fatal("late cancel", r, err)
	}
	b := partitionBytes(t, "ranges-p1.partition.gz")
	b[int(page)*PageSize+200] ^= 1
	in[1].Reader = bytes.NewReader(b)
	r, err = ScanPartitions(t.Context(), in, ScanOptions{}, func(PartitionEvent) error { return nil })
	if !errors.Is(err, ErrCorrupt) || r.Complete || r.CompletedPartitions != 1 || r.Records < 500 {
		t.Fatal("late corruption", r, err)
	}
	if _, err = ScanPartitions(t.Context(), in, ScanOptions{}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil callback", err)
	}
}
func FuzzPartitionDirectory(f *testing.F) {
	base := partitionBytes(f, "ranges-p0.partition.gz")
	inputs := []PartitionInput{}
	for i := 0; i < 3; i++ {
		b := partitionBytes(f, fmt.Sprintf("ranges-p%d.partition.gz", i))
		inputs = append(inputs, PartitionInput{Name: fmt.Sprintf("p%d", i), Reader: bytes.NewReader(b), Size: int64(len(b))})
	}
	for i := 0; i < 4; i++ {
		f.Add(uint8(i), uint32(i))
	}
	f.Fuzz(func(t *testing.T, field uint8, value uint32) {
		b := partitionSDIMutation(t, base, func(o map[string]any) {
			p := o["dd_object"].(map[string]any)["partitions"].([]any)[0].(map[string]any)
			switch field % 4 {
			case 0:
				p["number"] = value
			case 1:
				p["se_private_id"] = value
			case 2:
				p["indexes"].([]any)[0].(map[string]any)["index_opx"] = value
			case 3:
				p["parent_partition_id"] = value
			}
		})
		in := append([]PartitionInput(nil), inputs...)
		in[0].Reader = bytes.NewReader(b)
		set, err := InspectPartitions(t.Context(), in, ScanOptions{MaxPageReads: 1000, MaxEntries: 1000})
		if err != nil && set != nil {
			t.Fatal("partial metadata")
		}
	})
}
