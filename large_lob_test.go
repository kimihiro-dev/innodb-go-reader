package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func largeLOBFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/large_lob", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/large_lob", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err := json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestLargeLOBFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/large_lob/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256  string
			Rows          int
			ExpectedError string `json:"expected_error"`
		}
	}
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 6 {
		t.Fatal("expected six fixtures")
	}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := largeLOBFixture(t, c.Name)
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if c.ExpectedError != "" {
				if r != nil || !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "implementation limit") {
					t.Fatal("expected value limit without partial result", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows {
				t.Fatal("row count")
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/large_lob", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err := d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(want) != len(r.Records) {
				t.Fatal("SQL row count")
			}
			for i, rec := range r.Records {
				for k, col := range s.Columns {
					if want[i][k] == nil {
						if rec.Values[k] != nil {
							t.Fatal("expected NULL")
						}
						continue
					}
					if strings.HasSuffix(col.Type, "BLOB") {
						expected, err := hex.DecodeString(want[i][k].(string))
						if err != nil {
							t.Fatal(err)
						}
						v, ok := rec.Values[k].([]byte)
						if !ok || v == nil || !bytes.Equal(v, expected) {
							t.Fatalf("row %d column %s binary mismatch", i, col.Name)
						}
					} else if strings.HasSuffix(col.Type, "TEXT") {
						if rec.Values[k] != want[i][k] {
							t.Fatalf("row %d column %s text mismatch", i, col.Name)
						}
					} else {
						v, _ := json.Marshal(rec.Values[k])
						if string(v) != want[i][k].(json.Number).String() {
							t.Fatal("integer mismatch")
						}
					}
				}
				for _, f := range rec.External {
					var value []byte
					if v, ok := rec.Values[f.Column].([]byte); ok {
						value = v
					} else {
						value = []byte(rec.Values[f.Column].(string))
					}
					pos := 0
					for _, chunk := range f.Chunks {
						off := int(chunk.IndexPage)*PageSize + chunk.IndexOffset
						if be.Uint32(b[off+48:]) != chunk.PageNumber || int(be.Uint16(b[off+52:])) != chunk.Length {
							t.Fatal("index provenance")
						}
						pageData := b[int(chunk.PageNumber)*PageSize+chunk.Offset : int(chunk.PageNumber)*PageSize+chunk.Offset+chunk.Length]
						if !bytes.Equal(pageData, value[pos:pos+chunk.Length]) {
							t.Fatal("chunk order/data provenance")
						}
						pos += chunk.Length
					}
					if pos != int(f.Length) {
						t.Fatal("chunk length sum")
					}
				}
				if c.Name == "medium_index" {
					f := rec.External[0]
					wantChunks := []int{10, 11, 282, 283}
					wantPages := []int{0, 1, 1, 2}
					indexPages := map[uint32]bool{}
					for _, ch := range f.Chunks {
						if ch.IndexPage != f.FirstPage {
							indexPages[ch.IndexPage] = true
						}
					}
					if len(f.Chunks) != wantChunks[i] || len(indexPages) != wantPages[i] {
						t.Fatal("index capacity boundary", i, len(f.Chunks), len(indexPages))
					}
				}
				if c.Name == "long_limit" && len(rec.Values[1].([]byte)) != int(MaxLOBValueBytes) {
					t.Fatal("limit endpoint not tested")
				}
			}
		})
	}
}

func TestLOBTypeContracts(t *testing.T) {
	for _, kind := range []string{"TINYTEXT", "TINYBLOB", "TEXT", "BLOB", "MEDIUMTEXT", "MEDIUMBLOB", "LONGTEXT", "LONGBLOB"} {
		_, s := fixture(t, "lesson_rows")
		s.Columns[2] = Column{Name: "name", Type: kind, Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(kind, err)
		}
		for _, bad := range []Column{{Name: "name", Type: kind, MaxChars: 1}, {Name: "name", Type: kind, MaxBytes: 1}, {Name: "name", Type: kind, Unsigned: true}} {
			s.Columns[2] = bad
			if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatal("accepted attributes", kind)
			}
		}
	}
	if (Column{Type: "LONGTEXT"}).variableMaxBytes() != 4294967295 {
		t.Fatal("LONG SQL byte limit")
	}
	for _, test := range []struct {
		raw    string
		length int
		bad    bool
	}{{"7f", 127, false}, {"8080", 128, false}, {"ff80", 255, false}, {"0081", 0, true}} {
		raw, _ := hex.DecodeString(test.raw)
		b := append(make([]byte, dataStart), raw...)
		n, _, _, err := readVariableLength(b, len(b)-1, 255, true)
		if test.bad {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted oversized TINY")
			}
		} else if err != nil || n != test.length {
			t.Fatal("TINY length", n, err)
		}
	}
	f := ExternalField{Length: MaxLOBValueBytes + 1}
	if _, err := readExternal(bytes.NewReader(nil), 1<<30, &f); !errors.Is(err, ErrUnsupported) {
		t.Fatal("value limit must precede IO", err)
	}
}

func TestLOBIndexDamage(t *testing.T) {
	b, s := largeLOBFixture(t, "medium_index")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	f := r.Records[1].External[0]
	root := int(f.FirstPage) * PageSize
	ext := int(f.Chunks[10].IndexPage) * PageSize
	lastFirst := root + f.Chunks[9].IndexOffset
	f2 := r.Records[3].External[0]
	root2 := int(f2.FirstPage) * PageSize
	ext2 := int(f2.Chunks[len(f2.Chunks)-1].IndexPage) * PageSize
	cases := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"allocation-cycle", func(b []byte) { be.PutUint32(b[ext+12:], uint32(ext/PageSize)) }, ErrCorrupt},
		{"allocation-missing", func(b []byte) { be.PutUint32(b[root+12:], ^uint32(0)) }, ErrCorrupt},
		{"allocation-extra", func(b []byte) { be.PutUint32(b[ext+12:], uint32(ext/PageSize+1)) }, ErrUnsupported},
		{"allocation-outside-file", func(b []byte) { be.PutUint32(b[root+12:], uint32(len(b)/PageSize)) }, ErrCorrupt},
		{"index-wrong-type", func(b []byte) { be.PutUint16(b[ext+24:], 23) }, ErrUnsupported},
		{"index-space", func(b []byte) { be.PutUint32(b[ext+34:], s.SpaceID+1) }, ErrCorrupt},
		{"index-version", func(b []byte) { b[ext+38] = 1 }, ErrUnsupported},
		{"index-wrong-page-number", func(b []byte) { be.PutUint32(b[ext+4:], 0) }, ErrCorrupt},
		{"unaligned-slot", func(b []byte) { be.PutUint16(b[lastFirst+10:], 40) }, ErrCorrupt},
		{"slot-in-trailer", func(b []byte) { be.PutUint16(b[lastFirst+10:], 16359) }, ErrCorrupt},
		{"slot-in-header", func(b []byte) { be.PutUint16(b[lastFirst+10:], 38) }, ErrCorrupt},
		{"outside-owned-pages", func(b []byte) { be.PutUint32(b[lastFirst+6:], uint32(ext/PageSize+1)) }, ErrCorrupt},
		{"external-prev", func(b []byte) { be.PutUint16(b[ext+39+4:], 96) }, ErrCorrupt},
		{"external-history", func(b []byte) { be.PutUint32(b[ext+39+12:], 1) }, ErrCorrupt},
		{"external-data-length", func(b []byte) { be.PutUint16(b[ext+39+52:], 2) }, ErrCorrupt},
		{"external-data-repeat", func(b []byte) { be.PutUint32(b[ext+39+48:], f.FirstPage) }, ErrCorrupt},
		{"external-active-cycle", func(b []byte) { be.PutUint32(b[ext+39+6:], uint32(ext/PageSize)); be.PutUint16(b[ext+39+10:], 39) }, ErrCorrupt},
		{"count-exceeds-file-pages", func(b []byte) { be.PutUint32(b[root+64:], ^uint32(0)) }, ErrCorrupt},
		{"second-allocation-cycle", func(b []byte) { be.PutUint32(b[ext2+12:], be.Uint32(b[root2+12:])) }, ErrCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			tc.mutate(bad)
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatal("accepted damaged index", err)
			}
		})
	}
}

func FuzzLOBIndex(f *testing.F) {
	b, s := largeLOBFixture(f, "large_text")
	f.Add(uint32(5*PageSize+12), []byte{0, 0, 0, 5})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if got != nil && err != nil {
			t.Fatal("partial table")
		}
	})
}
