package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func floatFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/floating", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/floating", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestFloatFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/floating/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 4 {
		t.Fatal("expected four fixtures")
	}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := floatFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/floating", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows {
				t.Fatal("row count")
			}
			for i, rec := range r.Records {
				for k, col := range s.Columns {
					value, w := rec.Values[k], want[i][k]
					if w == nil {
						if value != nil {
							t.Fatal("NULL mismatch")
						}
						continue
					}
					switch col.Type {
					case "FLOAT", "DOUBLE":
						width := floatWidth(col.Type) * 8
						expected, err := strconv.ParseFloat(w.(json.Number).String(), width)
						if err != nil {
							t.Fatal(err)
						}
						var rawBits, wantBits uint64
						if width == 32 {
							v, ok := value.(float32)
							if !ok {
								t.Fatal("not float32")
							}
							rawBits = uint64(math.Float32bits(v))
							wantBits = uint64(math.Float32bits(float32(expected)))
						} else {
							v, ok := value.(float64)
							if !ok {
								t.Fatal("not float64")
							}
							rawBits = math.Float64bits(v)
							wantBits = math.Float64bits(expected)
						}
						if rawBits != wantBits {
							t.Fatalf("SQL bits row %d column %s: %x != %x", i, col.Name, rawBits, wantBits)
						}
						// Standard JSON must round-trip at the column's own width.
						encoded, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						decoded, err := strconv.ParseFloat(string(encoded), width)
						if err != nil {
							t.Fatal(err)
						}
						if width == 32 && uint64(math.Float32bits(float32(decoded))) != rawBits || width == 64 && math.Float64bits(decoded) != rawBits {
							t.Fatal("JSON bit loss")
						}
						if c.Name == "float_values" || c.Name == "double_values" {
							off := int(rec.PageNumber)*PageSize + rec.Offset + 17 + (k-1)*width/8
							if width == 32 {
								if uint64(binary.LittleEndian.Uint32(b[off:])) != rawBits {
									t.Fatal("raw FLOAT bits")
								}
							} else if binary.LittleEndian.Uint64(b[off:]) != rawBits {
								t.Fatal("raw DOUBLE bits")
							}
						}
					default:
						encoded, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						var got any
						d = json.NewDecoder(bytes.NewReader(encoded))
						d.UseNumber()
						if err = d.Decode(&got); err != nil {
							t.Fatal(err)
						}
						if got != w {
							t.Fatalf("mixed column %s mismatch", col.Name)
						}
					}
				}
			}
			if c.Name == "float_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("missing tree")
			}
			if c.Name == "float_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("missing LOB")
			}
			total += c.Rows
		})
	}
	if total != 890 {
		t.Fatal("fixture coverage", total)
	}
}

func TestFloatContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for _, kind := range []string{"FLOAT", "DOUBLE"} {
		good := Column{Name: "name", Type: kind, Nullable: true}
		for _, unsigned := range []bool{false, true} {
			s := base
			s.Columns = append([]Column(nil), base.Columns...)
			s.Columns[2] = good
			s.Columns[2].Unsigned = unsigned
			if _, err := s.validate(); err != nil {
				t.Fatal(err)
			}
		}
		for _, change := range []func(*Column){func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.MaxBytes = 1 }, func(c *Column) { c.Precision = 24 }, func(c *Column) { c.Scale = 2 }, func(c *Column) { c.Type = "REAL" }} {
			s := base
			s.Columns = append([]Column(nil), base.Columns...)
			s.Columns[2] = good
			change(&s.Columns[2])
			if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatal("bad attributes accepted")
			}
		}
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[0] = Column{Name: "id", Type: kind}
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("floating PK accepted")
		}
		width := floatWidth(kind)
		var patterns []uint64
		if width == 4 {
			patterns = []uint64{0, 0x80000000, 1, 0x80000001, 0x7f7fffff, 0xff7fffff, 0x7f800000, 0xff800000, 0x7fc00000, 0x7f800001}
		} else {
			patterns = []uint64{0, 1 << 63, 1, 1<<63 | 1, 0x7fefffffffffffff, 0xffefffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000000, 0x7ff0000000000001}
		}
		for i, bits := range patterns {
			b := make([]byte, width)
			if width == 4 {
				binary.LittleEndian.PutUint32(b, uint32(bits))
			} else {
				binary.LittleEndian.PutUint64(b, bits)
			}
			original := append([]byte(nil), b...)
			v, err := decodeFloat(b, good)
			if i >= 6 {
				if !errors.Is(err, ErrCorrupt) {
					t.Fatal("accepted nonfinite")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if width == 4 {
				if uint64(math.Float32bits(v.(float32))) != bits {
					t.Fatal("bits lost")
				}
			} else if math.Float64bits(v.(float64)) != bits {
				t.Fatal("bits lost")
			}
			if !bytes.Equal(b, original) {
				t.Fatal("input changed")
			}
			unsigned := good
			unsigned.Unsigned = true
			_, err = decodeFloat(b, unsigned)
			if i == 3 || i == 5 {
				if !errors.Is(err, ErrCorrupt) {
					t.Fatal("negative unsigned accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
		for _, n := range []int{0, width - 1, width + 1} {
			if _, err := decodeFloat(make([]byte, n), good); !errors.Is(err, ErrCorrupt) {
				t.Fatal("bad length")
			}
		}
	}
}

func TestFloatDamage(t *testing.T) {
	for _, name := range []string{"float_values", "double_values"} {
		b, s := floatFixture(t, name)
		r, err := Read(bytes.NewReader(b), int64(len(b)), s)
		if err != nil {
			t.Fatal(err)
		}
		width := floatWidth(s.Columns[1].Type)
		off := int(r.Records[0].PageNumber)*PageSize + r.Records[0].Offset + 17
		for _, inf := range []bool{false, true} {
			bad := append([]byte(nil), b...)
			if width == 4 {
				bits := uint32(0x7fc00000)
				if inf {
					bits = 0x7f800000
				}
				binary.LittleEndian.PutUint32(bad[off:], bits)
			} else {
				bits := uint64(0x7ff8000000000000)
				if inf {
					bits = 0xfff0000000000000
				}
				binary.LittleEndian.PutUint64(bad[off:], bits)
			}
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted corrupt float", err)
			}
		}
		sc := s
		sc.Columns = append([]Column(nil), s.Columns...)
		sc.Columns[1].Unsigned = true
		got, err := Read(bytes.NewReader(b), int64(len(b)), sc)
		if got != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("accepted negative unsigned column")
		}
	}
}

func FuzzFloat(f *testing.F) {
	b, s := floatFixture(f, "float_mixed")
	f.Add(uint32(4*PageSize+150), []byte{0xff, 0xff, 0xff, 0x7f})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && r != nil {
			t.Fatal("partial table")
		}
	})
}
