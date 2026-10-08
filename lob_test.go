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
	"unicode/utf8"
)

func lobFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/lob", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/lob", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err := json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestLOBFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/lob/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
			Level        uint16 `json:"root_level"`
		}
	}
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 3 {
		t.Fatal("expected three LOB fixtures")
	}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := lobFixture(t, c.Name)
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
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/lob", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err := d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(want) != len(r.Records) {
				t.Fatal("SQL count mismatch")
			}
			externalCount := 0
			splitUTF8 := false
			for i, rec := range r.Records {
				for k, col := range s.Columns {
					v := rec.Values[k]
					expected := want[i][k]
					if expected == nil {
						if v != nil {
							t.Fatal("expected NULL")
						}
						continue
					}
					switch col.Type {
					case "VARBINARY":
						w, err := hex.DecodeString(expected.(string))
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(v.([]byte), w) || v.([]byte) == nil {
							t.Fatalf("binary mismatch row %d", i)
						}
					case "VARCHAR":
						if v != expected {
							t.Fatalf("text mismatch row %d", i)
						}
					default:
						j, _ := json.Marshal(v)
						if string(j) != string(expected.(json.Number)) {
							t.Fatalf("integer mismatch row %d", i)
						}
					}
				}
				for _, f := range rec.External {
					externalCount++
					off := int(rec.PageNumber)*PageSize + f.Offset
					if !bytes.Equal(f.Reference[:], b[off:off+20]) || f.Offset+20 > rec.End {
						t.Fatal("reference provenance")
					}
					var joined []byte
					for _, chunk := range f.Chunks {
						off := int(chunk.PageNumber)*PageSize + chunk.Offset
						raw := b[off : off+chunk.Length]
						joined = append(joined, raw...)
						if s.Columns[f.Column].Type == "VARCHAR" && !utf8.Valid(raw) {
							splitUTF8 = true
						}
					}
					if len(joined) != int(f.Length) {
						t.Fatal("chunk lengths")
					}
					decoded, err := variableValue(s.Columns[f.Column], joined)
					if err != nil || !reflect.DeepEqual(decoded, rec.Values[f.Column]) {
						t.Fatal("chunks do not reconstruct value")
					}
				}
				if c.Name == "lob_sizes" {
					counts := []int{0, 1, 1, 2, 2, 3, 4, 0}
					got := 0
					for _, f := range rec.External {
						got += len(f.Chunks)
					}
					if got != counts[i] {
						t.Fatalf("row %d chunks %d, want %d", i, got, counts[i])
					}
				}
			}
			if externalCount == 0 {
				t.Fatal("no external values")
			}
			if c.Name == "lob_text" && !splitUTF8 {
				t.Fatal("fixture must split a UTF-8 character across pages")
			}
		})
	}
}

func TestLOBDamage(t *testing.T) {
	b, s := lobFixture(t, "lob_text")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[1]
	f := rec.External[0]
	ref := int(rec.PageNumber)*PageSize + f.Offset
	first := int(f.FirstPage) * PageSize
	node := first + f.Chunks[0].IndexOffset
	second := first + f.Chunks[1].IndexOffset
	data := int(f.Chunks[1].PageNumber) * PageSize
	tests := []struct {
		name   string
		change func([]byte)
		want   error
	}{
		{"ref-space", func(b []byte) { be.PutUint32(b[ref:], s.SpaceID+1) }, ErrCorrupt},
		{"ref-page-zero", func(b []byte) { be.PutUint32(b[ref+4:], 0) }, ErrCorrupt},
		{"ref-page-outside", func(b []byte) { be.PutUint32(b[ref+4:], uint32(len(b)/PageSize)) }, ErrCorrupt},
		{"ref-version", func(b []byte) { be.PutUint32(b[ref+8:], 2) }, ErrCorrupt},
		{"ref-modifying", func(b []byte) { b[ref+12] = 0x20 }, ErrUnsupported},
		{"ref-unused-high-length", func(b []byte) { b[ref+15] = 1 }, ErrCorrupt},
		{"ref-zero-length", func(b []byte) { be.PutUint32(b[ref+16:], 0) }, ErrCorrupt},
		{"ref-over-schema", func(b []byte) { be.PutUint32(b[ref+16:], ^uint32(0)) }, ErrCorrupt},
		{"ref-short-length", func(b []byte) { be.PutUint32(b[ref+16:], f.Length-1) }, ErrCorrupt},
		{"ref-long-length", func(b []byte) { be.PutUint32(b[ref+16:], f.Length+1) }, ErrCorrupt},
		{"first-wrong-type", func(b []byte) { be.PutUint16(b[first+24:], 10) }, ErrUnsupported},
		{"first-space", func(b []byte) { be.PutUint32(b[first+34:], 0) }, ErrCorrupt},
		{"first-page-number", func(b []byte) { be.PutUint32(b[first+4:], 0) }, ErrCorrupt},
		{"first-format", func(b []byte) { b[first+38] = 1 }, ErrUnsupported},
		{"first-flags", func(b []byte) { b[first+39] = 2 }, ErrUnsupported},
		{"first-version", func(b []byte) { be.PutUint32(b[first+40:], 2) }, ErrUnsupported},
		{"first-count-zero", func(b []byte) { be.PutUint32(b[first+64:], 0) }, ErrCorrupt},
		{"first-count-short", func(b []byte) { be.PutUint32(b[first+64:], 2) }, ErrCorrupt},
		{"external-index-page", func(b []byte) { be.PutUint32(b[first+68:], f.FirstPage+1) }, ErrCorrupt},
		{"bad-slot", func(b []byte) { be.PutUint16(b[first+72:], 97) }, ErrCorrupt},
		{"wrong-tail", func(b []byte) { be.PutUint16(b[first+78:], 96) }, ErrCorrupt},
		{"node-cycle", func(b []byte) { copy(b[node+6:node+12], b[first+68:first+74]) }, ErrCorrupt},
		{"node-prev", func(b []byte) { be.PutUint16(b[second+4:], 97) }, ErrCorrupt},
		{"node-versions", func(b []byte) { be.PutUint32(b[node+12:], 1) }, ErrCorrupt},
		{"node-version", func(b []byte) { be.PutUint32(b[node+56:], 2) }, ErrCorrupt},
		{"node-repeated-page", func(b []byte) { be.PutUint32(b[second+48:], f.FirstPage) }, ErrCorrupt},
		{"node-zero-length", func(b []byte) { be.PutUint16(b[node+52:], 0) }, ErrCorrupt},
		{"node-length-mismatch", func(b []byte) { be.PutUint16(b[node+52:], 10) }, ErrCorrupt},
		{"first-length", func(b []byte) { be.PutUint32(b[first+54:], 10) }, ErrCorrupt},
		{"data-length", func(b []byte) { be.PutUint32(b[data+39:], ^uint32(0)) }, ErrCorrupt},
		{"data-type", func(b []byte) { be.PutUint16(b[data+24:], 24) }, ErrUnsupported},
		{"data-trx", func(b []byte) { b[data+43] ^= 1 }, ErrCorrupt},
		{"invalid-text", func(b []byte) { b[first+696] = 0xff }, ErrCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			tt.change(bad)
			resealTestPages(bad)
			result, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if result != nil || !errors.Is(err, tt.want) {
				t.Fatalf("result present=%t err=%v, want %v", result != nil, err, tt.want)
			}
		})
	}
	if _, err := parseExternal(make([]byte, 19), 65535, s.SpaceID); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if _, err := parseExternal(make([]byte, 21), 65535, s.SpaceID); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func FuzzLOB(f *testing.F) {
	b, s := lobFixture(f, "lob_text")
	f.Add(uint32(5*PageSize+64), []byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, offset uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(offset)%len(bad):], change)
		resealTestPages(bad)
		result, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if result != nil && err != nil {
			t.Fatal("partial table")
		}
	})
}
