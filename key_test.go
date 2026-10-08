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

func keyFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/keys/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/keys/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestKeyFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/keys/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := keyFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/keys/"+c.Name+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(sdi.Records)+1 {
				t.Fatal("SDI count")
			}
			for i, r := range sdi.Records {
				var w struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &w); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{w.Type, w.ID}) || !reflect.DeepEqual(jsonTextValue(t, r.JSON), jsonTextValue(t, w.Object)) {
					t.Fatal("official SDI")
				}
			}

			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if meta.Schema == nil || !reflect.DeepEqual(*meta.Schema, s) {
				t.Fatalf("schema %+v issues %+v", meta.Schema, meta.Issues)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/keys/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows {
				t.Fatal("rows")
			}
			_, err = s.validate()
			if err != nil {
				t.Fatal(err)
			}

			for i, r := range got.Records {
				values := append([]any{}, r.Values...)
				for j, c := range s.Columns {
					if c.isBinary() && values[j] != nil {
						values[j] = strings.ToUpper(fmt.Sprintf("%x", values[j].([]byte)))
					}
				}
				encoded, _ := json.Marshal(values)
				if !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
					t.Fatalf("row %d: %s != %s", i, encoded, expected[i])
				}
				total++
			}
			if strings.HasSuffix(c.Name, "_tree") && got.Page.Level < 1 {
				t.Fatal("missing non-leaf coverage")
			}
			if c.Name == "keys_deep" && got.Page.Level < 2 {
				t.Fatalf("expected three levels, got %d", got.Page.Level)
			}
		})
	}
	t.Logf("%d real key rows", total)
}

func TestKeyContracts(t *testing.T) {
	_, s := keyFixture(t, "keys_lesson")
	for _, change := range []func(*Schema){
		func(s *Schema) { s.Columns[3].Collation = "utf8mb4_0900_ai_ci" },
		func(s *Schema) { s.Columns[3].Collation = "latin1_bin" },
		func(s *Schema) { s.Columns[3].Collation = "" },
		func(s *Schema) { s.Columns[0].Descending = true },
		func(s *Schema) { s.Columns[0].Collation = "utf8mb4_bin" },
		func(s *Schema) { s.Columns[2].MaxBytes = 3072 },
		func(s *Schema) { s.Columns[2].MaxBytes = 0 },
		func(s *Schema) { s.Columns[3].Nullable = true },
		func(s *Schema) { s.Columns[2] = Column{Name: "k", Type: "FLOAT"} },
	} {
		bad := s
		bad.Columns = append([]Column{}, s.Columns...)
		change(&bad)
		if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{{"a", "a ", 0}, {"a\x00", "a", -1}, {"a\x1f", "a", -1}, {"a!", "a", 1}, {"a  !", "a", 1}, {"", " ", 0}, {"\x80", "\xff", -1}} {
		for _, desc := range []bool{false, true} {
			schema := Schema{Columns: []Column{{Type: "VARCHAR", Descending: desc}}}
			want := tc.want
			if desc {
				want = -want
			}
			if got := compareIndexKey(indexKey{[]byte(tc.a)}, indexKey{[]byte(tc.b)}, schema, []int{0}); got != want {
				t.Fatal(tc, desc, got)
			}
		}
	}
}

func TestKeyDamage(t *testing.T) {
	for _, kind := range []string{"duplicate", "direction", "leaf-external", "node-external", "node-length", "node-pointer", "node-range", "invalid-text"} {
		t.Run(kind, func(t *testing.T) {
			b, s := keyFixture(t, "keys_deep")
			result, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			r := result.Records[0]
			n := result.Nodes[0]
			switch kind {
			case "duplicate":
				// Duplicate the final fixed-width member of two identical prefix tuples.
				found := false
				for i := 1; i < len(result.Records); i++ {
					a, z := result.Records[i-1], result.Records[i]
					if bytes.Equal(a.key[0], z.key[0]) && bytes.Equal(a.key[1], z.key[1]) {
						pos := int(z.PageNumber)*PageSize + z.Offset + len(z.key[0]) + len(z.key[1])
						copy(b[pos:pos+4], a.key[2])
						found = true
						break
					}
				}
				if !found {
					t.Fatal("no shared prefixes")
				}
			case "direction":
				s.Columns[2].Descending = false
			case "leaf-external":
				b[int(r.PageNumber)*PageSize+r.Offset-7] |= 0xc0
			case "node-external":
				b[int(n.PageNumber)*PageSize+n.Offset-7] |= 0xc0
			case "node-length":
				b[int(n.PageNumber)*PageSize+n.Offset-7] = 0x83
				b[int(n.PageNumber)*PageSize+n.Offset-8] = 0xff
			case "node-pointer":
				be.PutUint32(b[int(n.PageNumber)*PageSize+n.End-4:], 0)
			case "node-range":
				for _, node := range result.Nodes {
					if !node.Minimum {
						b[int(node.PageNumber)*PageSize+node.Offset] = 'z'
						break
					}
				}
			case "invalid-text":
				b[int(r.PageNumber)*PageSize+r.Offset] = 0xff
			}
			resealTestPages(b)
			got, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err == nil || got != nil {
				t.Fatalf("accepted damage %s", kind)
			}
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

func FuzzKeyOrder(f *testing.F) {
	f.Add([]byte("a\x00"), []byte("a "), false)
	f.Fuzz(func(t *testing.T, a, b []byte, desc bool) {
		if len(a) > 3072 || len(b) > 3072 {
			return
		}
		// Independent oracle pads full copies before bytes.Compare.
		n := max(len(a), len(b))
		x, y := bytes.Repeat([]byte{32}, n), bytes.Repeat([]byte{32}, n)
		copy(x, a)
		copy(y, b)
		want := bytes.Compare(x, y)
		if desc {
			want = -want
		}
		s := Schema{Columns: []Column{{Type: "VARCHAR", Descending: desc}}}
		if got := compareIndexKey(indexKey{a}, indexKey{b}, s, []int{0}); got != want {
			t.Fatal(got, want)
		}
	})
}
func FuzzKeyRead(f *testing.F) {
	b, s := keyFixture(f, "keys_lesson")
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 2*1024*1024 {
			return
		}
		got, err := Read(bytes.NewReader(b), int64(len(b)), s)
		if err != nil && got != nil {
			t.Fatal("partial result")
		}
	})
}

func TestKeyMetadataRejection(t *testing.T) {
	for _, kind := range []string{"collation", "prefix", "order", "hidden-order"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := keyFixture(t, "keys_utf8mb4_varchar_asc")
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			d := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
			d.UseNumber()
			if err = d.Decode(&doc); err != nil {
				t.Fatal(err)
			}
			table := doc["dd_object"].(map[string]any)
			elements := table["indexes"].([]any)[0].(map[string]any)["elements"].([]any)
			switch kind {
			case "collation":
				table["columns"].([]any)[2].(map[string]any)["collation_id"] = 255
			case "prefix":
				elements[0].(map[string]any)["length"] = 4
			case "order":
				elements[0].(map[string]any)["order"] = 99
			case "hidden-order":
				elements[2].(map[string]any)["order"] = 3
			}
			sdi.Records[0].JSON, _ = json.Marshal(doc)
			got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
			if err != nil {
				t.Fatal(err)
			}
			if got.Schema != nil || len(got.Issues) == 0 {
				t.Fatal("accepted unsupported metadata")
			}
		})
	}
}
