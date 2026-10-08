package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func instantFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/instant/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/instant/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestInstantFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/instant/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
			Unordered    bool
			Physical     struct{ DeleteMarked int }
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total := 0
	results := map[string]*Result{}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := instantFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil || meta.Schema == nil || !reflect.DeepEqual(s, *meta.Schema) {
				t.Fatal("schema", err)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			results[c.Name] = got
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/instant/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows || len(got.DeletedRecords) != c.Physical.DeleteMarked {
				t.Fatal("counts")
			}
			wanted, actual := map[string]int{}, map[string]int{}
			for _, row := range expected {
				canonical, _ := json.Marshal(jsonTextValue(t, row))
				wanted[string(canonical)]++
			}
			for i, r := range got.Records {
				values := append([]any{}, r.Values...)
				for j, v := range values {
					switch x := v.(type) {
					case []byte:
						values[j] = strings.ToUpper(fmt.Sprintf("%x", x))
					case JSONValue:
						values[j], err = x.JSON()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				encoded, err := json.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				canonical, _ := json.Marshal(jsonTextValue(t, encoded))
				actual[string(canonical)]++
				if !c.Unordered && !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
					t.Fatalf("row %d SQL mismatch", i)
				}
				total++
				version := uint8(0)
				if r.RowVersion != nil {
					version = *r.RowVersion
					if b[int(r.PageNumber)*PageSize+r.Offset-6] != version {
						t.Fatal("version source")
					}
				}
				if s.Instant != nil {
					want := []int{}
					for _, f := range s.Instant.Fields {
						if f.Column >= 0 && f.Added > version {
							want = append(want, f.Column)
						}
					}
					sort.Ints(want)
					if !slices.Equal(want, r.DefaultColumns) {
						t.Fatal("default provenance")
					}
				} else if r.RowVersion != nil || len(r.DefaultColumns) != 0 {
					t.Fatal("rebuilt layout")
				}
			}

			if c.Unordered && !reflect.DeepEqual(wanted, actual) {
				t.Fatal("SQL multiset")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/instant/"+c.Name+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(sdi.Records)+1 {
				t.Fatal("SDI count")
			}
			for i, r := range sdi.Records {
				var want struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &want); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{want.Type, want.ID}) || !reflect.DeepEqual(jsonTextValue(t, r.JSON), jsonTextValue(t, want.Object)) {
					t.Fatal("official SDI")
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	if len(manifest.Cases) != 17 || total != 3916 {
		t.Fatal("fixture matrix", len(manifest.Cases), total)
	}
	before := results["lesson_default_changed"]
	if before.Records[0].Values[0] != int32(77) || before.Records[3].Values[0] != int32(99) {
		t.Fatal("ADD default replaced by current default")
	}
	if results["tree_drop"].Page.Level == 0 {
		t.Fatal("missing nonleaf instant tree")
	}
	if results["hidden_drop"].Records[0].RowID == nil {
		t.Fatal("missing hidden identity")
	}
	r := results["versions_v64"].Records
	if r[0].RowVersion != nil || r[len(r)-1].RowVersion == nil || *r[len(r)-1].RowVersion != 64 {
		t.Fatal("version boundary")
	}
	t.Logf("%d snapshots, %d SQL rows", len(manifest.Cases), total)
}

func TestInstantSchemaDamage(t *testing.T) {
	b, original := instantFixture(t, "lesson_readd")
	cases := []struct {
		name   string
		change func(*Schema)
	}{
		{"version-zero", func(s *Schema) { s.Instant.Version = 0 }},
		{"version65", func(s *Schema) { s.Instant.Version = 65 }},
		{"version-incomplete", func(s *Schema) { s.Instant.Version++ }},
		{"position-duplicate", func(s *Schema) { s.Instant.Fields[1].Position = 0 }},
		{"position-gap", func(s *Schema) { s.Instant.Fields[1].Position = 1000 }},
		{"position-negative", func(s *Schema) { s.Instant.Fields[0].Position = -1 }},
		{"column-missing", func(s *Schema) { s.Instant.Fields = s.Instant.Fields[:len(s.Instant.Fields)-1] }},
		{"logical-index", func(s *Schema) { s.Instant.Fields[0].Column = 1000 }},
		{"dropped-current", func(s *Schema) { s.Instant.Fields[0].Dropped = 1 }},
		{"key-added", func(s *Schema) { s.Instant.Fields[0].Added = 1 }},
		{"drop-lifetime", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.Dropped > 0 {
					f.Added = f.Dropped
					return
				}
			}
		}},
		{"drop-definition", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.Dropped > 0 {
					f.DroppedColumn = nil
					return
				}
			}
		}},
		{"drop-type", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.DroppedColumn != nil {
					f.DroppedColumn.Type = "UNKNOWN"
					return
				}
			}
		}},
		{"default-missing", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.Added > 0 && f.Dropped == 0 {
					f.Default = nil
					return
				}
			}
		}},
		{"default-null", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.Column >= 0 && !s.Columns[f.Column].Nullable {
					f.Default = &InstantDefault{Null: true}
					return
				}
			}
		}},
		{"default-overflow", func(s *Schema) {
			for i := range s.Instant.Fields {
				f := &s.Instant.Fields[i]
				if f.Added > 0 && f.Dropped == 0 {
					f.Default = &InstantDefault{Data: make([]byte, 256)}
					return
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var s Schema
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			c.change(&s)
			got, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if got != nil || err == nil {
				t.Fatal("accepted bad layout")
			}
		})
	}
}

func TestInstantRecordDamage(t *testing.T) {
	original, s := instantFixture(t, "lesson_add")
	got, err := Read(bytes.NewReader(original), int64(len(original)), s)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Records[2]
	off := int(r.PageNumber)*PageSize + r.Offset
	for _, c := range []struct {
		name   string
		change func([]byte)
		want   error
	}{
		{"zero", func(b []byte) { b[off-6] = 0 }, ErrCorrupt},
		{"future", func(b []byte) { b[off-6] = 2 }, ErrCorrupt},
		{"above-limit", func(b []byte) { b[off-6] = 65 }, ErrCorrupt},
		{"legacy", func(b []byte) { b[off-5] = (b[off-5] &^ byte(0x40)) | 0x80 }, ErrUnsupported},
		{"both-flags", func(b []byte) { b[off-5] |= 0x80 }, ErrUnsupported},
		{"missing-version", func(b []byte) { b[off-5] &= ^byte(0x40) }, ErrCorrupt},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := append([]byte(nil), original...)
			c.change(b)
			resealTestPages(b)
			v, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if v != nil || !errors.Is(err, c.want) {
				t.Fatalf("result=%t err=%v", v != nil, err)
			}
		})
	}
	// Deletion preserves the complete physical record, including its version byte.
	b := append([]byte(nil), original...)
	b[off-5] |= 0x20
	resealTestPages(b)
	v, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(v.Records) != 2 || len(v.DeletedRecords) != 1 {
		t.Fatal("versioned delete mark", err)
	}
	d := v.DeletedRecords[0]
	if !bytes.Equal(d.Raw, b[int(d.PageNumber)*PageSize+d.Start:int(d.PageNumber)*PageSize+d.End]) {
		t.Fatal("deleted version raw")
	}
}

func TestInstantDroppedLOBNotRead(t *testing.T) {
	before, s := instantFixture(t, "lesson_before")
	r, err := Read(bytes.NewReader(before), int64(len(before)), s)
	if err != nil {
		t.Fatal(err)
	}
	old := r.Records[0].External[0]
	b, s := instantFixture(t, "lesson_drop")
	// The old row still contains the dropped column's local external reference.
	be.PutUint16(b[int(old.FirstPage)*PageSize+24:], 0)
	resealTestPages(b)
	got, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil || len(got.Records) != 5 {
		t.Fatal("followed dropped external value", err)
	}
	for _, r := range got.Records {
		if len(r.External) != 0 {
			t.Fatal("dropped external leaked")
		}
	}
}

func TestInstantMetadataDamage(t *testing.T) {
	original, _ := instantFixture(t, "lesson_readd")
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing-position", func(c map[string]any) {
			p := c["se_private_data"].(string)
			parts := strings.Split(p, ";")
			for i, v := range parts {
				if strings.HasPrefix(v, "physical_pos=") {
					parts[i] = ""
				}
			}
			c["se_private_data"] = strings.Join(parts, ";")
		}},
		{"position-duplicate", func(c map[string]any) {
			p := c["se_private_data"].(string)
			for _, v := range strings.Split(p, ";") {
				if strings.HasPrefix(v, "physical_pos=") {
					p = strings.Replace(p, v, "physical_pos=0", 1)
				}
			}
			c["se_private_data"] = p
		}},
		{"version-zero", func(c map[string]any) {
			c["se_private_data"] = strings.ReplaceAll(c["se_private_data"].(string), "version_added=3", "version_added=0")
		}},
		{"version-overflow", func(c map[string]any) {
			c["se_private_data"] = strings.ReplaceAll(c["se_private_data"].(string), "version_added=3", "version_added=65")
		}},
		{"default-hex", func(c map[string]any) {
			c["se_private_data"] = strings.ReplaceAll(c["se_private_data"].(string), "default=616761696e", "default=gg")
		}},
		{"default-conflict", func(c map[string]any) { c["se_private_data"] = c["se_private_data"].(string) + "default_null=1;" }},
		{"default-null-value", func(c map[string]any) {
			c["se_private_data"] = strings.ReplaceAll(c["se_private_data"].(string), "default=616761696e", "default_null=0")
		}},
		{"unknown-property", func(c map[string]any) { c["se_private_data"] = c["se_private_data"].(string) + "future_layout=1;" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sdi, err := ReadSDI(bytes.NewReader(original), int64(len(original)))
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]any
			decoder := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
			decoder.UseNumber()
			if err = decoder.Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			columns := envelope["dd_object"].(map[string]any)["columns"].([]any)
			tc.mutate(columns[0].(map[string]any))
			sdi.Records[0].JSON, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			m, err := inspectSDITable(bytes.NewReader(original), int64(len(original)), sdi)
			if err == nil && (m.Schema != nil || len(m.Issues) == 0) {
				t.Fatal("accepted bad SDI")
			}
		})
	}
}

func FuzzInstantRecord(f *testing.F) {
	b, s := instantFixture(f, "tree_drop")
	got, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		f.Fatal(err)
	}
	var page Page
	for _, p := range got.Pages {
		if p.Level == 0 {
			page = p
			break
		}
	}
	original := append([]byte(nil), b[int(page.Number)*PageSize:int(page.Number+1)*PageSize]...)
	pk, err := s.validate()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint16(120), []byte{0x40, 2, 0})
	f.Fuzz(func(t *testing.T, off uint16, change []byte) {
		if len(change) > PageSize {
			return
		}
		b := append([]byte(nil), original...)
		copy(b[int(off)%len(b):], change)
		_, _ = decodePage(b, page, s, pk)
	})
}
