package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func compositeFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/composite/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/composite/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestCompositeFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/composite/manifest.json")
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
	total, external := 0, 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := compositeFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/composite/"+c.Name+".sdi.json.gz"), &official); err != nil {
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
			if err = json.Unmarshal(unzip(t, "testdata/composite/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows {
				t.Fatal("rows")
			}
			pk, err := s.validate()
			if err != nil {
				t.Fatal(err)
			}
			width := 0
			for _, i := range pk {
				width += integerWidth(s.Columns[i].Type)
			}
			for i, r := range got.Records {
				encoded, err := json.Marshal(r.Values)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
					t.Fatalf("SQL row %d: %s != %s", i, encoded, expected[i])
				}
				offset := int(r.PageNumber)*PageSize + r.Offset
				for _, j := range pk {
					c := s.Columns[j]
					n := integerWidth(c.Type)
					v := decodeInteger(b[offset:offset+n], c.Unsigned)
					if v != r.Values[j] {
						t.Fatal("physical key order")
					}
					offset += n
				}
				if !bytes.Equal(b[offset:offset+6], r.Transaction[:]) || !bytes.Equal(b[offset+6:offset+13], r.RollPointer[:]) {
					t.Fatal("system position")
				}
				total++
				external += len(r.External)
			}
			for _, n := range got.Nodes {
				tuple, ok := n.Key.([]any)
				if !ok || len(tuple) != len(pk) || n.End-n.Offset != width+4 {
					t.Fatal("node tuple/width")
				}
				offset := int(n.PageNumber)*PageSize + n.Offset
				for i, j := range pk {
					c := s.Columns[j]
					w := integerWidth(c.Type)
					v := decodeInteger(b[offset:offset+w], c.Unsigned)
					if !reflect.DeepEqual(v, tuple[i]) {
						t.Fatal("node member")
					}
					offset += w
				}
				if be.Uint32(b[offset:offset+4]) != n.ChildPage {
					t.Fatal("node child offset")
				}
				page := b[int(n.PageNumber)*PageSize : int(n.PageNumber+1)*PageSize]
				if _, err := decodeNode(page, n.Offset, n.End-1, s, pk); !errors.Is(err, ErrCorrupt) {
					t.Fatal("truncated node", err)
				}
			}
			if c.Name == "composite_deep" && got.Page.Level < 2 {
				t.Fatal("expected three levels", got.Page.Level)
			}
			if c.Name == "composite_tree" && got.Page.Level == 0 {
				t.Fatal("tree")
			}
			t.Logf("rows=%d pages=%d level=%d nodes=%d", len(got.Records), len(got.Pages), got.Page.Level, len(got.Nodes))
		})
	}
	if total != 3614 || external != 1 {
		t.Fatal("coverage", total, external)
	}
}
func TestCompositeContracts(t *testing.T) {
	_, s := compositeFixture(t, "composite_lesson")
	for _, keys := range [][]string{nil, {}, {"a", "a"}, {"missing"}, {"a", "text"}, {"a", "n"}} {
		bad := s
		bad.PrimaryKeys = keys
		if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal(keys, err)
		}
	}
	bad := s
	bad.PrimaryKey = "a"
	if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	bad = s
	bad.PrimaryKeys = make([]string, 17)
	if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	b, old := fixture(t, "lesson_rows")
	single := old
	single.PrimaryKeys = []string{single.PrimaryKey}
	single.PrimaryKey = ""
	a, err := Read(bytes.NewReader(b), int64(len(b)), old)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Read(bytes.NewReader(b), int64(len(b)), single)
	if err != nil || !reflect.DeepEqual(a, result) {
		t.Fatal("single-key API", err)
	}
	// Same prefixes require later members; the complete uint64 range stays exact.
	if compareKey(orderedKey([]any{int64(-1), uint64(math.MaxUint64 - 1)}, []int{0, 1}), orderedKey([]any{int64(-1), uint64(math.MaxUint64)}, []int{0, 1})) >= 0 {
		t.Fatal("uint64 order")
	}
	if compareKey([]uint64{1, 2, 4}, []uint64{1, 3, 0}) >= 0 || compareKey([]uint64{1, 2, 4}, []uint64{1, 2, 4}) != 0 {
		t.Fatal("lexicographic order")
	}
}
func TestCompositeDamage(t *testing.T) {
	for _, kind := range []string{"duplicate", "later-key-reversed", "node-range", "child-range"} {
		t.Run(kind, func(t *testing.T) {
			name := "composite_lesson"
			if kind == "node-range" || kind == "child-range" {
				name = "composite_tree"
			}
			b, s := compositeFixture(t, name)
			rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate":
				a, z := rows.Records[0], rows.Records[1]
				src := int(a.PageNumber)*PageSize + a.Offset
				dst := int(z.PageNumber)*PageSize + z.Offset
				copy(b[dst:dst+13], b[src:src+13])
			case "later-key-reversed":
				// Rows 4 and 5 share a,b; make row 5 third member -2, below row 4 (-1).
				r := rows.Records[4]
				pos := int(r.PageNumber)*PageSize + r.Offset + 10
				copy(b[pos:pos+3], []byte{0x7f, 0xff, 0xfe})
			case "node-range":
				// Keep the prefix intact; move a finite separator's last member above its child's first key.
				n := rows.Nodes[1]
				pos := int(n.PageNumber)*PageSize + n.Offset + 4
				be.PutUint32(b[pos:pos+4], be.Uint32(b[pos:pos+4])+1)
			case "child-range":
				// A leaf's first key falls outside the complete parent tuple range.
				n := rows.Nodes[1]
				var r Record
				for _, v := range rows.Records {
					if v.PageNumber == n.ChildPage {
						r = v
						break
					}
				}
				pos := int(r.PageNumber)*PageSize + r.Offset
				be.PutUint16(b[pos:pos+2], 0)
			}
			resealTestPages(b)
			got, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("missed damage or partial", err)
			}
			if (kind == "node-range" || kind == "child-range") && !strings.Contains(err.Error(), "outside parent range") {
				t.Fatal("did not exercise tuple range", err)
			}
			if auto, err := ReadAuto(bytes.NewReader(b), int64(len(b))); auto != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("automatic entry", err)
			}
		})
	}
}
func TestCompositeMetadataDamage(t *testing.T) {
	for _, kind := range []string{"invalid-order", "prefix", "duplicate", "physical-order"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := compositeFixture(t, "composite_lesson")
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var e map[string]any
			d := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
			d.UseNumber()
			if err = d.Decode(&e); err != nil {
				t.Fatal(err)
			}
			elements := e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["elements"].([]any)
			switch kind {
			case "invalid-order":
				elements[1].(map[string]any)["order"] = 99
			case "prefix":
				elements[1].(map[string]any)["length"] = 1
			case "duplicate":
				elements[1].(map[string]any)["column_opx"] = elements[0].(map[string]any)["column_opx"]
			case "physical-order":
				elements[3].(map[string]any)["column_opx"], elements[4].(map[string]any)["column_opx"] = elements[4].(map[string]any)["column_opx"], elements[3].(map[string]any)["column_opx"]
			}
			sdi.Records[0].JSON, _ = json.Marshal(e)
			got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
			if err == nil && (got.Schema != nil || len(got.Issues) == 0) {
				t.Fatal("accepted invalid primary metadata")
			}
		})
	}
}
func FuzzCompositeOrder(f *testing.F) {
	f.Add(int64(-1), uint64(math.MaxUint64), int64(0), uint64(0))
	f.Fuzz(func(t *testing.T, a int64, b uint64, c int64, d uint64) {
		got := compareKey(orderedKey([]any{a, b}, []int{0, 1}), orderedKey([]any{c, d}, []int{0, 1}))
		want := 0
		if a < c || a == c && b < d {
			want = -1
		}
		if a > c || a == c && b > d {
			want = 1
		}
		if got != want {
			t.Fatal(got, want)
		}
	})
}

func FuzzCompositeRead(f *testing.F) {
	b, s := compositeFixture(f, "composite_lesson")
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

// Encode numeric test tuples so the oracle exercises the physical comparator.
func orderedKey(values []any, columns []int) []uint64 {
	out := make([]uint64, len(columns))
	for i, c := range columns {
		out[i] = integerOrder(values[c])
	}
	return out
}
func compareKey(a, b []uint64) int {
	s := Schema{}
	pk := []int{}
	x, y := indexKey{}, indexKey{}
	for i := range a {
		s.Columns = append(s.Columns, Column{Type: "BIGINT", Unsigned: true})
		pk = append(pk, i)
		ab, bb := make([]byte, 8), make([]byte, 8)
		be.PutUint64(ab, a[i])
		be.PutUint64(bb, b[i])
		x = append(x, ab)
		y = append(y, bb)
	}
	return compareIndexKey(x, y, s, pk)
}

// integerOrder is compared only within one validated primary-key type. Signed
// values are shifted into unsigned order without arithmetic overflow.
func integerOrder(value any) uint64 {
	var signed int64
	switch v := value.(type) {
	case int8:
		signed = int64(v)
	case int16:
		signed = int64(v)
	case int32:
		signed = int64(v)
	case int64:
		signed = v
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	}
	return uint64(signed) ^ (uint64(1) << 63)
}
