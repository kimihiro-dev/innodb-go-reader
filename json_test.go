package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Compare JSON numbers as exact rationals, never via float64. This permits
// equivalent exponent notation while retaining uint64 and decimal precision.
func jsonTextValue(t testing.TB, b []byte) any {
	t.Helper()
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			r, ok := new(big.Rat).SetString(string(x))
			if !ok {
				t.Fatal(x)
			}
			return struct{ Number string }{r.RatString()}
		case []any:
			for i := range x {
				x[i] = normalize(x[i])
			}
			return x
		case map[string]any:
			for k := range x {
				x[k] = normalize(x[k])
			}
			return x
		}
		return v
	}
	return normalize(v)
}

func TestJSONFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/json/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	total, external, large, decimal, temporal, opaque := 0, 0, 0, 0, 0, 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := unzip(t, "testdata/json/"+c.Name+".ibd.gz")
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA")
			}
			raw, err := os.ReadFile("testdata/json/" + c.Name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var schema Schema
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if meta.Schema == nil || !reflect.DeepEqual(*meta.Schema, schema) {
				t.Fatalf("schema: %+v", meta)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), schema)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var expected []struct {
				ID           int32
				SQLNull      bool `json:"sql_null"`
				Text         *string
				Type         string
				Depth, Bytes int
				Note         string
			}
			if err := json.Unmarshal(unzip(t, "testdata/json/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != len(expected) || len(expected) != c.Rows {
				t.Fatal("rows")
			}
			for i, r := range got.Records {
				want := expected[i]
				total++
				external += len(r.External)
				if r.Values[0] != want.ID || r.Values[2] != want.Note {
					t.Fatal("adjacent column", i)
				}
				if want.SQLNull {
					if r.Values[1] != nil {
						t.Fatal("SQL NULL")
					}
					continue
				}
				v, ok := r.Values[1].(JSONValue)
				if !ok {
					t.Fatalf("row %d not JSONValue: %T", i, r.Values[1])
				}
				storedBytes := r.End - (r.Offset + 17) - len(want.Note)
				if len(r.External) != 0 {
					storedBytes = int(r.External[0].Length)
				}
				if storedBytes != want.Bytes {
					t.Fatal("JSON_STORAGE_SIZE mismatch", storedBytes, want.Bytes)
				}
				text, err := v.JSON()
				if err != nil {
					t.Fatal(err)
				}
				if want.Text == nil || !reflect.DeepEqual(jsonTextValue(t, text), jsonTextValue(t, []byte(*want.Text))) {
					t.Fatalf("row %d SQL JSON mismatch got=%s want=%v", i, text, want.Text)
				}
				kind := map[string]string{"integer": "INTEGER", "unsigned": "UNSIGNED INTEGER", "null": "NULL", "boolean": "BOOLEAN", "string": "STRING", "double": "DOUBLE", "decimal": "DECIMAL", "array": "ARRAY", "object": "OBJECT"}[v.Kind]
				if kind != want.Type {
					t.Fatalf("row %d JSON_TYPE %s != %s", i, kind, want.Type)
				}
				var walk func(JSONValue) int
				walk = func(v JSONValue) int {
					if v.BinaryType == 1 || v.BinaryType == 3 {
						large++
					}
					if v.Kind == "decimal" {
						decimal++
						if v.Opaque == nil || len(v.Opaque.Data) < 3 || v.Opaque.Precision == 0 {
							t.Fatal("decimal evidence")
						}
					}
					if v.Kind == "date" || v.Kind == "time" || v.Kind == "datetime" || v.Kind == "timestamp" {
						temporal++
						if v.Opaque == nil || len(v.Opaque.Data) != 8 {
							t.Fatal("packed temporal")
						}
					}
					if v.Kind == "opaque" {
						opaque++
					}
					depth := 1
					for _, m := range v.Members {
						if n := 1 + walk(m.Value); n > depth {
							depth = n
						}
					}
					for _, e := range v.Elements {
						if n := 1 + walk(e); n > depth {
							depth = n
						}
					}
					return depth
				}
				if depth := walk(v); depth != want.Depth {
					t.Fatal("JSON_DEPTH", depth, want.Depth)
				}
			}
			if c.Name == "json_tree" && got.Page.Level < 1 {
				t.Fatal("expected non-leaf root")
			}
		})
	}
	if total != 636 || external < 1 || large < 3 || decimal < 2 || temporal < 3 || opaque < 3 {
		t.Fatalf("coverage rows=%d external=%d large=%d decimal=%d temporal=%d opaque=%d", total, external, large, decimal, temporal, opaque)
	}
	t.Logf("rows=%d external=%d large=%d decimal=%d temporal=%d opaque=%d", total, external, large, decimal, temporal, opaque)
}

func TestJSONScalarWidths(t *testing.T) {
	cases := []struct {
		hex   string
		value any
	}{
		{"05ffff", int64(-1)}, {"06ffff", uint64(65535)}, {"0700000080", int64(-2147483648)},
		{"08ffffffff", uint64(4294967295)}, {"090000000000000080", int64(math.MinInt64)},
		{"0affffffffffffffff", uint64(math.MaxUint64)}, {"0b0000000000000080", math.Copysign(0, -1)},
	}
	for _, c := range cases {
		b, _ := hex.DecodeString(c.hex)
		v, err := DecodeJSON(b)
		if err != nil || !reflect.DeepEqual(v.Value, c.value) {
			t.Fatal(c.hex, v, err)
		}
		if v.Kind == "double" && !math.Signbit(v.Value.(float64)) {
			t.Fatal("negative zero lost")
		}
	}
	// Small array [1, "x"]: payload header=4, value entries=6, string at offset10.
	b := []byte{2, 2, 0, 12, 0, 5, 1, 0, 12, 10, 0, 1, 'x'}
	v, err := DecodeJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	text, err := v.JSON()
	if err != nil || string(text) != `[1,"x"]` {
		t.Fatal(string(text), err)
	}
	// Opaque preserves an unknown field type and owns a copy of its bytes.
	b = []byte{15, 200, 2, 0, 255}
	v, err = DecodeJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	b[3] = 42
	if !bytes.Equal(v.Opaque.Data, []byte{0, 255}) {
		t.Fatal("opaque aliases input")
	}
	text, err = v.JSON()
	if err != nil || string(text) != `"base64:type200:AP8="` {
		t.Fatal(string(text), err)
	}
}

func TestJSONDamage(t *testing.T) {
	cases := []string{"04", "0403", "0501", "0b000000000000f07f", "0c02fffe", "0c808080808000", "0fffffffffff1f", "0201000400", "02010007000cffff", "0001000b000a00010004000061", "040000", "0ff60100", "0ff603010280", "0f0a0100"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			b, _ := hex.DecodeString(s)
			v, err := DecodeJSON(b)
			if !errors.Is(err, ErrCorrupt) || !reflect.DeepEqual(v, JSONValue{}) {
				t.Fatal("corruption accepted/partial", v, err)
			}
		})
	}
	// Large inline int32, then corrupt the container size and offset.
	b := []byte{3, 1, 0, 0, 0, 13, 0, 0, 0, 7, 0, 0, 0, 128}
	v, err := DecodeJSON(b)
	if err != nil || v.Elements[0].Value != int64(-2147483648) {
		t.Fatal(v, err)
	}
	if _, err = DecodeJSON(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err = DecodeJSON(make([]byte, int(MaxLOBValueBytes)+1)); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	// Unknown outer tag is corruption, whereas arbitrary opaque field types retain bytes.
	if _, err = DecodeJSON([]byte{255}); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

func jsonNestedArray(depth int) []byte {
	b := []byte{4, 0}
	for i := 0; i < depth; i++ {
		if b[0] == 4 {
			b = []byte{2, 1, 0, 7, 0, 4, 0, 0}
			continue
		}
		size := 7 + len(b) - 1
		next := []byte{2, 1, 0, byte(size), byte(size >> 8), b[0], 7, 0}
		b = append(next, b[1:]...)
	}
	return b
}
func TestJSONLimits(t *testing.T) {
	if _, err := DecodeJSON(jsonNestedArray(99)); err != nil {
		t.Fatal(err)
	}
	if v, err := DecodeJSON(jsonNestedArray(100)); !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(v, JSONValue{}) {
		t.Fatal(err)
	}
	for _, count := range []int{MaxJSONNodes - 1, MaxJSONNodes} {
		size := 8 + count*5
		b := make([]byte, size+1)
		b[0] = 3
		jsonLE.PutUint32(b[1:], uint32(count))
		jsonLE.PutUint32(b[5:], uint32(size))
		for i := 0; i < count; i++ {
			b[9+i*5] = 4
		}
		_, err := DecodeJSON(b)
		if count < MaxJSONNodes && err != nil || count == MaxJSONNodes && !errors.Is(err, ErrUnsupported) {
			t.Fatal(count, err)
		}
	}
	// Exact byte-limit success, including the tag and four-byte length prefix.
	n := int(MaxLOBValueBytes) - 5
	b := make([]byte, n+5)
	b[0] = 12
	for i := 0; i < 4; i++ {
		b[1+i] = byte(n >> (7 * i) & 127)
		if i != 3 {
			b[1+i] |= 128
		}
	}
	for i := 5; i < len(b); i++ {
		b[i] = 'x'
	}
	if v, err := DecodeJSON(b); err != nil || len(v.Value.(string)) != n {
		t.Fatal("byte limit boundary", err)
	}
}

func FuzzJSON(f *testing.F) {
	for _, b := range [][]byte{{4, 0}, {0, 0, 0, 4, 0}, {2, 2, 0, 12, 0, 5, 1, 0, 12, 10, 0, 1, 'x'}, {15, 200, 2, 0, 255}, jsonNestedArray(4)} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		v, err := DecodeJSON(b)
		if err != nil {
			if !reflect.DeepEqual(v, JSONValue{}) {
				t.Fatal("partial JSON")
			}
			return
		}
		text, err := v.JSON()
		if err != nil || !json.Valid(text) {
			t.Fatal("invalid display", err)
		}
		if strings.Contains(v.Kind, " ") {
			t.Fatal("invalid kind")
		}
	})
}

func TestJSONOpaqueDetails(t *testing.T) {
	b := unzip(t, "testdata/json/json_opaque.ibd.gz")
	rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/json/json_opaque.types.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected []struct {
		ID    int32
		Types []*string
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows.Records {
		v := r.Values[1].(JSONValue)
		nodes := v.Elements
		if v.Kind != "array" {
			nodes = []JSONValue{v}
		}
		if r.Values[0] != expected[i].ID {
			t.Fatal("type evidence row")
		}
		for j, node := range nodes {
			want := strings.ToUpper(node.Kind)
			if node.Kind == "opaque" {
				want = "BLOB"
			}
			if expected[i].Types[j] == nil || *expected[i].Types[j] != want {
				t.Fatal("SQL subtype mismatch", want)
			}
			o := node.Opaque
			if o == nil || node.BinaryType != 15 {
				t.Fatal("opaque origin lost")
			}
			if node.Kind == "decimal" {
				precision, scale, value := 37, 10, "12345678901234567890.1234567890"
				if i == 1 {
					precision, scale, value = 13, 4, "-0.0001"
				}
				if o.Precision != precision || o.Scale != scale || o.Decoded != value {
					t.Fatal("independent decimal storage evidence")
				}
			}
		}
	}
	// TIMESTAMP shares DATETIME's packed representation, not Unix seconds.
	packed := uint64((((2024*13+2)<<5|29)<<17)|(12<<12)|(34<<6)|56)<<24 | 123456
	payload := make([]byte, 8)
	jsonLE.PutUint64(payload, packed)
	v, err := DecodeJSON(append([]byte{15, 7, 8}, payload...))
	if err != nil || v.Kind != "timestamp" || v.Opaque.Decoded != "2024-02-29 12:34:56.123456" {
		t.Fatal(v, err)
	}
	// Internal JSON decimal capacity is nine groups, beyond SQL column precision65.
	payload = make([]byte, 38)
	payload[0] = 81
	payload[2] = 0x80
	payload[37] = 1
	v, err = DecodeJSON(append([]byte{15, 246, 38}, payload...))
	if err != nil || v.Opaque.Decoded != "1" || v.Opaque.Precision != 81 {
		t.Fatal(v, err)
	}
	v, err = DecodeJSON([]byte{15, 253, 2, 'h', 'i'})
	if err != nil || v.Kind != "opaque" || v.Opaque.FieldType != 253 {
		t.Fatal(v, err)
	}
	text, err := v.JSON()
	if err != nil || string(text) != `"hi"` {
		t.Fatal(string(text), err)
	}
}

func TestJSONReadDamage(t *testing.T) {
	for _, name := range []string{"json_lesson", "json_large"} {
		t.Run(name, func(t *testing.T) {
			b := unzip(t, "testdata/json/"+name+".ibd.gz")
			rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if name == "json_lesson" {
				r := rows.Records[1]
				b[int(r.PageNumber)*PageSize+r.Offset+17] = 255
			} else {
				r := rows.Records[0]
				if len(r.External) == 0 {
					t.Fatal("expected wholly external JSON")
				}
				chunk := r.External[0].Chunks[0]
				b[int(chunk.PageNumber)*PageSize+chunk.Offset] = 255
			}
			resealTestPages(b)
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupt JSON returned rows", err)
			}
		})
	}
}

func TestJSONContainerDamage(t *testing.T) {
	// Duplicate keys in a small object with two inline null values.
	b := []byte{0, 2, 0, 20, 0, 18, 0, 1, 0, 19, 0, 1, 0, 4, 0, 0, 4, 0, 0, 'a', 'a'}
	if _, err := DecodeJSON(b); !errors.Is(err, ErrCorrupt) {
		t.Fatal("duplicate key", err)
	}
	// Two non-inline strings cannot share the same offset.
	b = []byte{2, 2, 0, 14, 0, 12, 10, 0, 12, 10, 0, 1, 'a', 1, 'b'}
	if _, err := DecodeJSON(b); !errors.Is(err, ErrCorrupt) {
		t.Fatal("overlap", err)
	}
	// A partial update may leave an unreferenced byte before a string.
	b = []byte{2, 1, 0, 10, 0, 12, 8, 0, 0, 1, 'a'}
	if v, err := DecodeJSON(b); err != nil || len(v.Elements) != 1 || v.Elements[0].Value != "a" {
		t.Fatal("fragmentation", err)
	}
	for _, n := range []int64{math.MinInt64, -1, 1000000, (839 << 12) << 24} {
		b = make([]byte, 11)
		b[0] = 15
		b[1] = 11
		b[2] = 8
		jsonLE.PutUint64(b[3:], uint64(n))
		_, err := DecodeJSON(b)
		// -1 is a valid negative one-microsecond TIME, not a complement encoding.
		if n == -1 {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrCorrupt) {
			t.Fatal("time range", n, err)
		}
	}
}

func TestJSONFragmentedContainers(t *testing.T) {
	// Synthetic small array: logical order differs from physical payload order;
	// unreferenced bytes remain both before and after the two strings.
	b := []byte{2, 2, 0, 17, 0, 12, 14, 0, 12, 11, 0, 0, 1, 'b', 0, 1, 'a', 0}
	v, err := DecodeJSON(b)
	if err != nil || len(v.Elements) != 2 || v.Elements[0].Value != "a" || v.Elements[1].Value != "b" {
		t.Fatal("reordered values", err)
	}
	for _, off := range []byte{9, 12, 15, 17} {
		bad := append([]byte(nil), b...)
		bad[9] = off
		if _, err := DecodeJSON(bad); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("offset %d: %v", off, err)
		}
	}
	// Key bytes and a string payload must not overlap, even with valid pointers.
	object := []byte{0, 1, 0, 13, 0, 11, 0, 1, 0, 12, 11, 0, 1, 'x'}
	if _, err := DecodeJSON(object); !errors.Is(err, ErrCorrupt) {
		t.Fatal("key/value overlap", err)
	}
	// Empty key occupies zero bytes; a removed value may leave unused tail bytes.
	object = []byte{0, 1, 0, 13, 0, 11, 0, 0, 0, 4, 0, 0, 255, 255}
	v, err = DecodeJSON(object)
	if err != nil || len(v.Members) != 1 || v.Members[0].Key != "" || v.Members[0].Value.Kind != "null" {
		t.Fatal("empty key and tail", err)
	}
}
