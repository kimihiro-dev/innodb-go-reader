package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func integerFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/integers", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/integers", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err := json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestIntegerFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/integers/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err := json.Unmarshal(j, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cases) != 10 {
		t.Fatal("expected ten integer primary-key types")
	}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := integerFixture(t, c.Name)
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || r.Page.Level != 1 {
				t.Fatal("missing rows or internal nodes")
			}
			pk, _ := s.validate()
			width := integerWidth(s.Columns[pk[0]].Type)
			var expected [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/integers", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err := d.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			if len(expected) != len(r.Records) {
				t.Fatal("SQL row count")
			}
			signedTypes := map[string]any{"TINYINT": int8(0), "SMALLINT": int16(0), "MEDIUMINT": int32(0), "INT": int32(0), "BIGINT": int64(0)}
			unsignedTypes := map[string]any{"TINYINT": uint8(0), "SMALLINT": uint16(0), "MEDIUMINT": uint32(0), "INT": uint32(0), "BIGINT": uint64(0)}
			for i, rec := range r.Records {
				raw, err := json.Marshal(rec.Values)
				if err != nil {
					t.Fatal(err)
				}
				var got []any
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if err := d.Decode(&got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, expected[i]) {
					t.Fatalf("SQL row %d: got %s want %v", i, raw, expected[i])
				}
				for col, v := range rec.Values {
					if v == nil || s.Columns[col].Type == "VARCHAR" {
						continue
					}
					types := signedTypes
					if s.Columns[col].Unsigned {
						types = unsignedTypes
					}
					if reflect.TypeOf(v) != reflect.TypeOf(types[s.Columns[col].Type]) {
						t.Fatalf("wrong Go type %T", v)
					}
				}
				off := int(rec.PageNumber)*PageSize + rec.Offset + width
				if !bytes.Equal(rec.Transaction[:], b[off:off+6]) || !bytes.Equal(rec.RollPointer[:], b[off+6:off+13]) {
					t.Fatal("system field offset")
				}
			}
			if c.Name == "bigint_unsigned" {
				highNode := false
				for _, node := range r.Nodes {
					if !node.Minimum && node.Key.(uint64) > uint64(1<<63-1) {
						highNode = true
					}
				}
				if !highNode || r.Records[len(r.Records)-1].Values[pk[0]] != ^uint64(0) {
					t.Fatal("missing unsigned upper-half navigation/max endpoint")
				}
			}
			for _, node := range r.Nodes {
				if node.End-node.Offset != width+4 || reflect.TypeOf(node.Key) != reflect.TypeOf(r.Records[0].Values[pk[0]]) {
					t.Fatal("node key layout/type")
				}
				page := b[int(node.PageNumber)*PageSize : int(node.PageNumber+1)*PageSize]
				if _, err := decodeNode(page, node.Offset, node.End-1, s, pk); !errors.Is(err, ErrCorrupt) {
					t.Fatal("accepted truncated child pointer")
				}
			}
			// Duplicate physical key bytes must fail for every width and sign.
			bad := append([]byte(nil), b...)
			first, second := r.Records[0], r.Records[1]
			a := int(first.PageNumber)*PageSize + first.Offset
			z := int(second.PageNumber)*PageSize + second.Offset
			copy(bad[z:z+width], bad[a:a+width])
			resealTestPages(bad)
			if got, err := Read(bytes.NewReader(bad), int64(len(bad)), s); got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted duplicate key", err)
			}
			// A finite separator raised by one excludes its original first child row.
			bad = append([]byte(nil), b...)
			node := r.Nodes[1]
			off := int(node.PageNumber)*PageSize + node.Offset
			for i := width - 1; i >= 0; i-- {
				bad[off+i]++
				if bad[off+i] != 0 {
					break
				}
			}
			resealTestPages(bad)
			if got, err := Read(bytes.NewReader(bad), int64(len(bad)), s); got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted wrong parent range", err)
			}
		})
	}
}

func TestIntegerEncoding(t *testing.T) {
	cases := []struct {
		hex      string
		unsigned bool
		want     any
	}{
		{"00", false, int8(-128)}, {"7f", false, int8(-1)}, {"80", false, int8(0)}, {"ff", false, int8(127)},
		{"0000", false, int16(-32768)}, {"ffff", false, int16(32767)},
		{"000000", false, int32(-8388608)}, {"7fffff", false, int32(-1)}, {"800000", false, int32(0)}, {"ffffff", false, int32(8388607)},
		{"0000000000000000", false, int64(-1 << 63)}, {"7fffffffffffffff", false, int64(-1)}, {"8000000000000000", false, int64(0)}, {"ffffffffffffffff", false, int64(1<<63 - 1)},
		{"ff", true, uint8(255)}, {"ffff", true, uint16(65535)}, {"ffffff", true, uint32(16777215)}, {"ffffffff", true, uint32(4294967295)},
		{"8000000000000000", true, uint64(1 << 63)}, {"ffffffffffffffff", true, ^uint64(0)},
	}
	for _, c := range cases {
		b, _ := hex.DecodeString(c.hex)
		if got := decodeInteger(b, c.unsigned); got != c.want {
			t.Fatalf("%s unsigned=%t: %v (%T), want %v", c.hex, c.unsigned, got, got, c.want)
		}
	}
}

func TestIntegerSchemaAndTruncation(t *testing.T) {
	b, s := integerFixture(t, "bigint_unsigned")
	for _, change := range []func(*Schema){
		func(s *Schema) { s.Columns[0].MaxChars = 1 },
		func(s *Schema) { s.Columns[len(s.Columns)-1].Unsigned = true },
		func(s *Schema) { s.Columns[5].Nullable = true },
	} {
		bad := s
		bad.Columns = append([]Column(nil), s.Columns...)
		change(&bad)
		if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("accepted schema", err)
		}
	}
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[0]
	page := b[int(rec.PageNumber)*PageSize : int(rec.PageNumber+1)*PageSize]
	for _, limit := range []int{rec.Offset + 7, rec.Offset + 20, rec.End - 1} {
		if _, err := decodeRecord(page, rec.Offset, limit, s, []int{5}); !errors.Is(err, ErrCorrupt) {
			t.Fatal("accepted truncated record", err)
		}
	}
}

func FuzzIntegerTree(f *testing.F) {
	b, s := integerFixture(f, "bigint_unsigned")
	f.Add(uint32(4*PageSize+125), []byte{255, 255, 255, 255, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, offset uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(offset)%len(bad):], change)
		resealTestPages(bad)
		result, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && result != nil {
			t.Fatal("partial result")
		}
	})
}
