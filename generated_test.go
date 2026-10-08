package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func generatedFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/generated/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/generated/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestGeneratedFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/generated/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256      string
			Rows              int
			Unordered, Reject bool
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total := 0
	results := map[string]*MaterializedResult{}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := generatedFixture(t, c.Name)
			reader := bytes.NewReader(b)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			meta, err := InspectTable(reader, int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadMaterializedAuto(reader, int64(len(b)))
			if c.Reject {
				if got != nil || !errors.Is(err, ErrUnsupported) || meta.Schema != nil || meta.MaterializedSchema != nil {
					t.Fatal("accepted internal functional column", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if meta.MaterializedSchema == nil || !reflect.DeepEqual(s, *meta.MaterializedSchema) {
					x, _ := json.Marshal(meta.MaterializedSchema)
					y, _ := json.Marshal(s)
					t.Fatalf("schema mismatch\ngot %s\nwant %s", x, y)
				}
				manual, err := ReadMaterialized(reader, int64(len(b)), s)
				if err != nil || !reflect.DeepEqual(got, manual) {
					t.Fatal("manual/auto", err)
				}
				if !reflect.DeepEqual(got.Columns, s.Columns) || !reflect.DeepEqual(got.VirtualColumns, s.VirtualColumns) {
					t.Fatal("column contract")
				}
				results[c.Name] = got
				if len(got.Result.Records) != c.Rows {
					t.Fatal("row count")
				}
				var expected []json.RawMessage
				if err = json.Unmarshal(unzip(t, "testdata/generated/"+c.Name+".expected.json.gz"), &expected); err != nil {
					t.Fatal(err)
				}
				wantSet, gotSet := map[string]int{}, map[string]int{}
				for _, row := range expected {
					v, _ := json.Marshal(jsonTextValue(t, row))
					wantSet[string(v)]++
				}
				for i, r := range got.Result.Records {
					if len(r.Values) != len(got.Columns) {
						t.Fatal("value width")
					}
					values := append([]any{}, r.Values...)
					for j, v := range values {
						if x, ok := v.([]byte); ok {
							values[j] = strings.ToUpper(fmt.Sprintf("%x", x))
						}
					}
					encoded, err := json.Marshal(values)
					if err != nil {
						t.Fatal(err)
					}
					canonical, _ := json.Marshal(jsonTextValue(t, encoded))
					gotSet[string(canonical)]++
					if !c.Unordered && !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
						t.Fatalf("SQL row %d", i)
					}
					for j := range r.TextBytes {
						if j < 0 || j >= len(got.Columns) || !got.Columns[j].isText() {
							t.Fatal("text column mapping")
						}
					}
					for _, x := range r.External {
						if x.Column < 0 || x.Column >= len(got.Columns) || !got.Columns[x.Column].isVariable() {
							t.Fatal("external column mapping")
						}
					}
					total++
				}
				if !reflect.DeepEqual(wantSet, gotSet) {
					t.Fatal("SQL multiset")
				}
			}
			full, fullErr := ReadAuto(reader, int64(len(b)))
			if c.Reject || len(s.VirtualColumns) > 0 {
				if full != nil || !errors.Is(fullErr, ErrUnsupported) || meta.Schema != nil || len(meta.Issues) == 0 {
					t.Fatal("full row silently incomplete", fullErr)
				}
				if !c.Reject {
					full, fullErr = Read(reader, int64(len(b)), s)
					if full != nil || !errors.Is(fullErr, ErrUnsupported) {
						t.Fatal("explicit full row accepted virtual", fullErr)
					}
				}
			} else if fullErr != nil || !reflect.DeepEqual(full, got.Result) || meta.Schema == nil || len(meta.Issues) != 0 {
				t.Fatal("full stored row", fullErr)
			}
			// Full official SDI and independent SQL index identities.
			sdi, err := ReadSDI(reader, int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/generated/"+c.Name+".sdi.json.gz"), &official); err != nil {
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
				var envelope struct {
					Kind   string `json:"dd_object_type"`
					Object struct {
						Columns []ddColumn `json:"columns"`
					} `json:"dd_object"`
				}
				if err = json.Unmarshal(want.Object, &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Kind == "Table" {
					if len(envelope.Object.Columns) != len(meta.Columns) {
						t.Fatal("column metadata count")
					}
					for j, c := range envelope.Object.Columns {
						m := meta.Columns[j]
						if m.Virtual != c.Virtual || m.Invisible != (c.Hidden == 4) || m.GenerationExpression != c.Expression {
							t.Fatal("column classification")
						}
					}
				}
			}
			var indexes []struct {
				Name  string
				Root  uint32 `json:"root_page"`
				ID    uint64 `json:"index_id"`
				Space uint32 `json:"space_id"`
				Type  uint32
			}
			if err = json.Unmarshal(unzip(t, "testdata/generated/"+c.Name+".indexes.json.gz"), &indexes); err != nil {
				t.Fatal(err)
			}
			if len(indexes) != len(meta.Indexes) {
				t.Fatal("index count")
			}
			for _, want := range indexes {
				found := false
				for _, idx := range meta.Indexes {
					if idx.ID == want.ID {
						found = true
						if idx.RootPage != want.Root || idx.SpaceID != want.Space || idx.Clustered != (want.Type&1 != 0) || !idx.RootVerified {
							t.Fatal("index identity")
						}
					}
				}
				if !found {
					t.Fatal("SQL index absent")
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	if len(manifest.Cases) != 14 || total != 1232 {
		t.Fatal("matrix", len(manifest.Cases), total)
	}
	if results["tree_instant"].Result.Page.Level == 0 || len(results["tree_instant"].Result.Nodes) == 0 {
		t.Fatal("nonleaf tree missing")
	}
	if results["hidden_initial"].Result.Records[0].RowID == nil {
		t.Fatal("hidden identity missing")
	}
	m := results["mixed_instant"]
	if len(m.Result.Records[0].DefaultColumns) != 2 || len(m.Result.Records[0].External) != 2 || !m.Columns[1].Invisible || m.VirtualColumns[0].Ordinal != 4 || !m.VirtualColumns[1].Invisible {
		t.Fatal("instant/LOB/invisible evidence")
	}
	if results["mixed_initial"].Result.Records[1].Values[3] != nil {
		t.Fatal("stored SQL NULL")
	}
	t.Logf("%d snapshots, %d SQL rows, one functional-index rejection", len(manifest.Cases), total)
}

func TestMaterializedLegacySchema(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	full, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, auto := range []bool{false, true} {
		var got *MaterializedResult
		if auto {
			got, err = ReadMaterializedAuto(bytes.NewReader(b), int64(len(b)))
		} else {
			got, err = ReadMaterialized(bytes.NewReader(b), int64(len(b)), s)
		}
		if err != nil || !reflect.DeepEqual(got.Result, full) || !reflect.DeepEqual(got.Columns, s.Columns) || len(got.VirtualColumns) != 0 {
			t.Fatal("legacy contract", err)
		}
	}
}

func TestGeneratedMetadataDamage(t *testing.T) {
	b, _ := generatedFixture(t, "mixed_instant")
	for _, tc := range []struct {
		name    string
		mutate  func(map[string]any)
		corrupt bool
	}{
		{"missing-expression", func(d map[string]any) { d["columns"].([]any)[3].(map[string]any)["generation_expression"] = "" }, true},
		{"internal-hidden", func(d map[string]any) { d["columns"].([]any)[3].(map[string]any)["hidden"] = 3 }, false},
		{"unknown-hidden", func(d map[string]any) { d["columns"].([]any)[3].(map[string]any)["hidden"] = 99 }, false},
		{"virtual-physical-position", func(d map[string]any) {
			c := d["columns"].([]any)[3].(map[string]any)
			c["se_private_data"] = c["se_private_data"].(string) + "physical_pos=3;"
		}, false},
		{"virtual-default", func(d map[string]any) {
			c := d["columns"].([]any)[3].(map[string]any)
			c["se_private_data"] = c["se_private_data"].(string) + "default_null=1;"
		}, false},
		{"virtual-as-stored", func(d map[string]any) { d["columns"].([]any)[3].(map[string]any)["is_virtual"] = false }, false},
		{"stored-as-virtual", func(d map[string]any) { d["columns"].([]any)[5].(map[string]any)["is_virtual"] = true }, false},
		{"stored-internal", func(d map[string]any) { d["columns"].([]any)[5].(map[string]any)["hidden"] = 2 }, false},
		{"unsupported-stored-type", func(d map[string]any) { d["columns"].([]any)[5].(map[string]any)["type"] = 999 }, false},
		{"clustered-virtual", func(d map[string]any) {
			d["indexes"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)["column_opx"] = 3
		}, false},
		{"virtual-table-identity", func(d map[string]any) { d["columns"].([]any)[3].(map[string]any)["se_private_data"] = "table_id=1;" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var e map[string]any
			decoder := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
			decoder.UseNumber()
			if err = decoder.Decode(&e); err != nil {
				t.Fatal(err)
			}
			tc.mutate(e["dd_object"].(map[string]any))
			sdi.Records[0].JSON, err = json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			m, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
			if tc.corrupt {
				if m != nil || !errors.Is(err, ErrCorrupt) {
					t.Fatal("corrupt metadata accepted", err)
				}
			} else if err == nil && (m.Schema != nil || m.MaterializedSchema != nil || len(m.Issues) == 0) {
				t.Fatal("materialized bypassed unsupported layout")
			}
		})
	}
}

func TestMaterializedSchemaAndFailures(t *testing.T) {
	b, original := generatedFixture(t, "mixed_instant")
	for _, tc := range []struct {
		name   string
		change func(*Schema)
	}{
		{"name-empty", func(s *Schema) { s.VirtualColumns[0].Name = "" }},
		{"name-stored", func(s *Schema) { s.VirtualColumns[0].Name = s.Columns[0].Name }},
		{"name-duplicate", func(s *Schema) { s.VirtualColumns[1].Name = s.VirtualColumns[0].Name }},
		{"expression-empty", func(s *Schema) { s.VirtualColumns[0].Expression = "" }},
		{"ordinal-zero", func(s *Schema) { s.VirtualColumns[0].Ordinal = 0 }},
		{"ordinal-overflow", func(s *Schema) { s.VirtualColumns[1].Ordinal = 10000 }},
		{"ordinal-duplicate", func(s *Schema) { s.VirtualColumns[1].Ordinal = s.VirtualColumns[0].Ordinal }},
		{"ordinal-reversed", func(s *Schema) { s.VirtualColumns[0], s.VirtualColumns[1] = s.VirtualColumns[1], s.VirtualColumns[0] }},
		{"virtual-key", func(s *Schema) { s.PrimaryKey = s.VirtualColumns[0].Name }},
		{"too-many-columns", func(s *Schema) { s.VirtualColumns = make([]VirtualColumn, 1018) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var s Schema
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			tc.change(&s)
			got, err := ReadMaterialized(bytes.NewReader(b), int64(len(b)), s)
			if got != nil || !errors.Is(err, ErrUnsupported) {
				t.Fatal("bad explicit schema", err)
			}
		})
	}
	good, err := ReadMaterializedAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	// A damaged LOB is reached only after metadata and clustered rows succeed.
	bad := append([]byte(nil), b...)
	page := good.Result.Records[0].External[0].FirstPage
	bad[int(page)*PageSize+100] ^= 1
	for _, auto := range []bool{false, true} {
		var got *MaterializedResult
		if auto {
			got, err = ReadMaterializedAuto(bytes.NewReader(bad), int64(len(bad)))
		} else {
			got, err = ReadMaterialized(bytes.NewReader(bad), int64(len(bad)), original)
		}
		if got != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("partial result on LOB failure", err)
		}
	}
	got, err := ReadMaterializedAuto(bytes.NewReader(b[:PageSize]), int64(len(b)))
	if got != nil || err == nil {
		t.Fatal("partial result on I/O error")
	}
	// Reading must not alter caller-owned schema or its INSTANT mappings.
	raw, _ := json.Marshal(original)
	if _, err = ReadMaterialized(bytes.NewReader(b), int64(len(b)), original); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if !bytes.Equal(raw, after) {
		t.Fatal("schema mutated")
	}
}

func FuzzMaterializedRead(f *testing.F) {
	b, s := generatedFixture(f, "mixed_instant")
	f.Add(uint16(120), []byte{0x40, 1, 0})
	f.Fuzz(func(t *testing.T, off uint16, change []byte) {
		if len(change) > PageSize {
			return
		}
		data := append([]byte(nil), b...)
		page := data[int(s.RootPage)*PageSize : int(s.RootPage+1)*PageSize]
		copy(page[int(off)%PageSize:], change)
		resealTestPages(data)
		got, err := ReadMaterialized(bytes.NewReader(data), int64(len(data)), s)
		if err != nil && got != nil {
			t.Fatal("partial result")
		}
		if err == nil {
			for _, r := range got.Result.Records {
				if len(r.Values) != len(got.Columns) {
					t.Fatal("column mismatch")
				}
			}
		}
	})
}
