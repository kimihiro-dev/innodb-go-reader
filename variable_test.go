package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func variableFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/variable", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/variable", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err := json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestVariableFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/variable/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256  string
			Rows          int
			Level         uint16 `json:"root_level"`
			ExpectedError string `json:"expected_error"`
		}
	}
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 7 {
		t.Fatal("expected seven variable fixtures")
	}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := variableFixture(t, c.Name)
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)

			if err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || r.Page.Level != c.Level {
				t.Fatal("row count/root level")
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/variable", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err := d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(want) != len(r.Records) {
				t.Fatal("SQL count mismatch")
			}
			for i, rec := range r.Records {
				encoded, err := json.Marshal(rec.Values)
				if err != nil {
					t.Fatal(err)
				}
				var jsonValues []any
				d := json.NewDecoder(bytes.NewReader(encoded))
				d.UseNumber()
				if err := d.Decode(&jsonValues); err != nil {
					t.Fatal(err)
				}
				for k, col := range s.Columns {
					v := rec.Values[k]
					if col.Type == "VARBINARY" && want[i][k] != nil {
						expected, err := hex.DecodeString(want[i][k].(string))
						if err != nil {
							t.Fatal(err)
						}
						actual, ok := v.([]byte)
						if !ok || actual == nil || !bytes.Equal(actual, expected) {
							t.Fatalf("row %d col %s binary mismatch", i, col.Name)
						}
						encoded, ok := jsonValues[k].(string)
						if !ok {
							t.Fatal("binary JSON must be string")
						}
						decoded, err := base64.StdEncoding.DecodeString(encoded)
						if err != nil || !bytes.Equal(decoded, expected) {
							t.Fatal("binary JSON lost bytes")
						}
					} else if !reflect.DeepEqual(jsonValues[k], want[i][k]) {
						t.Fatalf("row %d col %s mismatch", i, col.Name)
					}
				}
			}
		})
	}
}

func TestVariableLengthEncoding(t *testing.T) {
	tests := []struct {
		name, raw     string
		maximum, want int
		failure       error
	}{
		{"short-zero", "00", 255, 0, nil}, {"short-127", "7f", 255, 127, nil},
		{"short-128", "80", 255, 128, nil}, {"short-255", "ff", 255, 255, nil},
		{"utf8-short-252", "fc", 252, 252, nil}, {"large-zero", "00", 256, 0, nil},
		{"large-127", "7f", 256, 127, nil}, {"large-128", "8080", 256, 128, nil},
		{"large-255", "ff80", 256, 255, nil}, {"large-256", "0081", 256, 256, nil},
		{"length-1024", "0084", 4096, 1024, nil},
		{"external", "14c0", 65535, 20, nil},
		{"short-exceeds-schema", "ff", 252, 0, ErrCorrupt},
		{"long-exceeds-schema", "0181", 256, 0, ErrCorrupt},
		{"noncanonical", "7f80", 256, 0, ErrCorrupt},
		{"missing-second", "80", 256, 0, ErrCorrupt},
		{"missing-first", "", 256, 0, ErrCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := hex.DecodeString(tt.raw)
			b := append(make([]byte, dataStart), raw...)
			got, next, external, err := readVariableLength(b, len(b)-1, uint64(tt.maximum), false)
			if tt.failure != nil {
				if !errors.Is(err, tt.failure) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || got != tt.want || next != dataStart-1 || external != (tt.name == "external") {
				t.Fatalf("got=%d next=%d err=%v", got, next, err)
			}
		})
	}
}

func TestVariableSchema(t *testing.T) {
	_, s := variableFixture(t, "length_rows")
	changes := []func(*Schema){
		func(s *Schema) { s.Columns[1].MaxChars = -1 }, func(s *Schema) { s.Columns[1].MaxChars = 16384 },
		func(s *Schema) { s.Columns[1].MaxBytes = 1 }, func(s *Schema) { s.Columns[1].Unsigned = true },
		func(s *Schema) { s.Columns[3].MaxBytes = -1 }, func(s *Schema) { s.Columns[3].MaxBytes = 65536 },
		func(s *Schema) { s.Columns[3].MaxChars = 1 }, func(s *Schema) { s.Columns[3].Unsigned = true },
		func(s *Schema) { s.Columns[2].MaxBytes = 1 }, func(s *Schema) { s.PrimaryKey = "b255"; s.Columns[3].Nullable = true },
	}
	for i, change := range changes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			bad := s
			bad.Columns = append([]Column(nil), s.Columns...)
			change(&bad)
			if _, err := bad.validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatal("accepted bad schema", err)
			}
		})
	}
	s.Columns[1].MaxChars = 16383
	s.Columns[5].MaxBytes = 65535
	if _, err := s.validate(); err != nil {
		t.Fatal("rejected permitted declared maxima", err)
	}
}

func TestVariableDamageAndOwnership(t *testing.T) {
	b, s := variableFixture(t, "length_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[9]
	page := b[int(rec.PageNumber)*PageSize : int(rec.PageNumber+1)*PageSize]
	if _, err := decodeRecord(page, rec.Offset, rec.End-1, s, []int{2}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("accepted truncated payload", err)
	}
	// Change only the UTF-8 text, leaving metadata and binary bytes untouched.
	bad := append([]byte(nil), b...)
	bad[int(rec.PageNumber)*PageSize+rec.Offset+4+13] = 0xff
	resealTestPages(bad)
	if got, err := Read(bytes.NewReader(bad), int64(len(bad)), s); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("accepted invalid UTF-8", err)
	}
	small := s
	small.Columns = append([]Column(nil), s.Columns...)
	small.Columns[1].MaxChars = 64
	if _, err := Read(bytes.NewReader(b), int64(len(b)), small); !errors.Is(err, ErrCorrupt) {
		t.Fatal("accepted oversized character count", err)
	}
	// Direct record decoder must not retain a mutable view of caller page bytes.
	decoded, err := decodeRecord(page, rec.Offset, int(r.Page.HeapTop), s, []int{2})
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), decoded.Values[5].([]byte)...)
	for i := rec.Offset; i < rec.End; i++ {
		page[i] = 0
	}
	if !bytes.Equal(decoded.Values[5].([]byte), before) {
		t.Fatal("binary value aliases input")
	}
}

func FuzzVariableTree(f *testing.F) {
	b, s := variableFixture(f, "variable_tree")
	f.Add(uint32(5*PageSize+120), []byte{0xff, 0xc0, 0x80})
	f.Fuzz(func(t *testing.T, offset uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(offset)%len(bad):], change)
		resealTestPages(bad)
		r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && r != nil {
			t.Fatal("partial result")
		}
	})
}

func TestVariableFragmentAccounting(t *testing.T) {
	b, s := variableFixture(t, "variable_tree")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range r.Pages {
		if p.Free != 0 || p.Garbage == 0 {
			continue
		}
		found = true
		live := 0
		for _, rec := range r.Records {
			if rec.PageNumber == p.Number {
				live += rec.End - rec.Start
			}
		}
		if live != int(p.HeapTop)-dataStart-int(p.Garbage) {
			t.Fatal("fragment accounting")
		}
		bad := append([]byte(nil), b...)
		be.PutUint16(bad[int(p.Number)*PageSize+46:], p.Garbage+1)
		resealTestPages(bad)
		if got, err := Read(bytes.NewReader(bad), int64(len(bad)), s); got != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("accepted inconsistent garbage bytes", err)
		}
	}
	if !found {
		t.Fatal("fixture must contain fragments with empty free chain")
	}
}
