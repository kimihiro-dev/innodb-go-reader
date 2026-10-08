package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCharsetFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/charset/manifest.json")
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
	total, external, zero, dict := 0, 0, 0, 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := unzip(t, "testdata/charset/"+c.Name+".ibd.gz")
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/charset/"+c.Name+".sdi.json.gz"), &official); err != nil {
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

			raw, err := os.ReadFile("testdata/charset/" + c.Name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var schema Schema
			if err = json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if meta.Schema == nil || !reflect.DeepEqual(*meta.Schema, schema) {
				t.Fatalf("schema got %+v; issues %+v; want %+v", meta.Schema, meta.Issues, schema)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), schema)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var wire []struct {
				ID    int32
				Note  string
				Cells []struct {
					Hex, Text *string
					FullHex   *string `json:"full_hex"`
					FullText  *string `json:"full_text"`
					Chars     *int
					Number    json.Number
				}
			}
			if err = json.Unmarshal(unzip(t, "testdata/charset/"+c.Name+".expected.json.gz"), &wire); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(wire) != c.Rows {
				t.Fatal("rows")
			}
			for i, r := range got.Records {
				w := wire[i]
				total++
				external += len(r.External)
				if r.Values[0] != w.ID || r.Values[len(r.Values)-1] != w.Note {
					t.Fatal("adjacent column", i)
				}
				for j, want := range w.Cells {
					col := schema.Columns[j+1]
					v := r.Values[j+1]
					raw, present := r.TextBytes[j+1]
					if want.Hex == nil {
						if v != nil || present {
							t.Fatal("NULL", j)
						}
						continue
					}
					if col.isBinary() {
						if v == nil || !strings.EqualFold(hex.EncodeToString(v.([]byte)), *want.Hex) {
							t.Fatal("binary", j)
						}
						zero++
						continue
					}
					if want.Text == nil || v != *want.Text {
						t.Fatalf("row %d col %s: text %v != %v", i, col.Name, v, want.Text)
					}
					if col.Type == "ENUM" {
						if !reflect.DeepEqual(jsonTextValue(t, []byte(fmt.Sprint(r.EnumIndexes[j+1]))), jsonTextValue(t, []byte(want.Number))) {
							t.Fatal("enum ordinal")
						}
						dict++
						continue
					}
					if col.Type == "SET" {
						if !reflect.DeepEqual(jsonTextValue(t, []byte(fmt.Sprint(r.SetMasks[j+1]))), jsonTextValue(t, []byte(want.Number))) {
							t.Fatal("set mask")
						}
						dict++
						continue
					}
					if !present || raw == nil {
						t.Fatal("missing original bytes", j)
					}
					if col.Type == "CHAR" {
						stored, ok := r.CharStorage[j+1]
						if !ok || strings.TrimRight(stored, " ") != v {
							t.Fatal("CHAR physical text")
						}
						padded := stored + strings.Repeat(" ", col.MaxChars-utf8.RuneCountInString(stored))
						rawPad := append(append([]byte{}, raw...), bytes.Repeat([]byte{' '}, col.MaxChars-utf8.RuneCountInString(stored))...)
						if padded != *want.FullText || !strings.EqualFold(hex.EncodeToString(rawPad), *want.FullHex) {
							t.Fatal("SQL padded CHAR", i, j)
						}
						if col.MaxChars == 0 {
							zero++
						}
					} else if !strings.EqualFold(hex.EncodeToString(raw), *want.Hex) || utf8.RuneCountInString(v.(string)) != *want.Chars {
						t.Fatal("SQL HEX/character count", i, j)
					}
				}
			}
			if c.Name == "charset_tree" && len(got.Pages) < 2 {
				t.Fatal("tree")
			}
		})
	}
	t.Logf("rows=%d external=%d zero-width checks=%d dictionary checks=%d", total, external, zero, dict)
	if total != 1064 || external < 8 || zero == 0 || dict != 90 {
		t.Fatal("coverage", total, external, zero, dict)
	}
}

func TestCharsetDecoding(t *testing.T) {
	for _, tc := range []struct {
		cs   string
		data []byte
		want string
	}{{"latin1", []byte{0x80, 0x81, 0x8d, 0x8f, 0x90, 0x9d, 0x9f, 0xff}, "€\u0081\u008d\u008f\u0090\u009dŸÿ"}, {"ascii", []byte{0, 0x7f}, "\x00\x7f"}, {"utf8mb3", []byte("\uffff中"), "\uffff中"}, {"utf8mb4", []byte("😀"), "😀"}} {
		v, err := decodeText(tc.cs, tc.data)
		if err != nil || v != tc.want {
			t.Fatal(tc.cs, v, err)
		}
	}
	for _, cs := range []string{"utf8mb3", "utf8mb4"} {
		for _, b := range [][]byte{{0xc0, 0xaf}, {0xed, 0xa0, 0x80}, {0xe4, 0xb8}, {0xff}, {0xf4, 0x90, 0x80, 0x80}} {
			if _, err := decodeText(cs, b); !errors.Is(err, ErrCorrupt) {
				t.Fatal(cs, b, err)
			}
		}
	}
	if _, err := decodeText("utf8mb3", []byte("😀")); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if _, err := decodeText("ascii", []byte{128}); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if _, err := decodeText("gbk", []byte("a")); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if (Column{Charset: "utf8"}).charsetName() != "utf8mb3" {
		t.Fatal("alias")
	}
	r := Record{}
	b := []byte("text")
	r.retainTextBytes(0, b)
	b[0] = 'X'
	if string(r.TextBytes[0]) != "text" {
		t.Fatal("alias")
	}
	r.retainTextBytes(1, nil)
	if r.TextBytes[1] == nil {
		t.Fatal("empty raw bytes")
	}
}
func TestCharsetSchema(t *testing.T) {
	for _, c := range []Column{{Name: "v", Type: "INT", Charset: "ascii"}, {Name: "v", Type: "VARCHAR", Charset: "gbk", MaxChars: 1}, {Name: "v", Type: "CHAR", Charset: "latin1", MaxChars: -1}, {Name: "v", Type: "VARCHAR", Charset: "utf8mb3", MaxChars: 21846}} {
		s := Schema{Columns: []Column{{Name: "id", Type: "INT"}, c}, PrimaryKey: "id", RootPage: 4, SpaceID: 1, IndexID: 1}
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal(c, err)
		}
	}
	for _, cs := range []string{"utf8mb4", "utf8mb3", "ascii", "latin1"} {
		c := Column{Type: "CHAR", Charset: cs, MaxChars: 5}
		if _, err := variableValue(c, []byte("a")); !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
		if _, err := variableValue(c, []byte("aaaaaa")); !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
	}
}
func FuzzCharset(f *testing.F) {
	f.Add([]byte("中文😀"), uint8(0))
	f.Add([]byte{0x80, 0x81, 0xff}, uint8(1))
	f.Fuzz(func(t *testing.T, b []byte, selector uint8) {
		cs := []string{"utf8mb4", "utf8mb3", "ascii", "latin1"}[selector%4]
		text, err := decodeText(cs, b)
		if err == nil && !utf8.ValidString(text) {
			t.Fatal("invalid output")
		}
		if cs == "latin1" && (err != nil || utf8.RuneCountInString(text) != len(b)) {
			t.Fatal("latin1 total mapping")
		}
	})
}

func TestCharsetReadDamage(t *testing.T) {
	for _, name := range []string{"charset_ascii_general_ci", "external_utf8mb3"} {
		t.Run(name, func(t *testing.T) {
			b := unzip(t, "testdata/charset/"+name+".ibd.gz")
			rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			r := rows.Records[len(rows.Records)-1]
			offset := int(r.PageNumber)*PageSize + r.Offset + 17
			if len(r.External) > 0 {
				ch := r.External[0].Chunks[0]
				offset = int(ch.PageNumber)*PageSize + ch.Offset
			}
			b[offset] = 0xff
			resealTestPages(b)
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("partial or missed invalid encoding", err)
			}
		})
	}
	b := unzip(t, "testdata/charset/charset_latin1_bin.ibd.gz")
	rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	// Ten nullable columns use two bitmap bytes. In this row, c is fixed,
	// v and t each consume one zero length; the following byte is CHAR(0).
	r := rows.Records[1]
	b[int(r.PageNumber)*PageSize+r.Offset-6-2-2] = 1
	resealTestPages(b)
	if got, err := ReadAuto(bytes.NewReader(b), int64(len(b))); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("nonzero CHAR(0) length", err)
	}
}
func TestCharsetDictionary(t *testing.T) {
	for _, tc := range []struct {
		cs, label string
		ok        bool
	}{{"ascii", "€", false}, {"utf8mb3", "😀", false}, {"latin1", "中", false}, {"latin1", "\u0080", false}, {"latin1", "€\u0081Ÿ", true}, {"utf8mb4", "😀", true}} {
		c := Column{Name: "label", Type: "ENUM", Charset: tc.cs, EnumValues: []string{tc.label}}
		s := Schema{Columns: []Column{{Name: "id", Type: "INT"}, c}, PrimaryKey: "id", RootPage: 4, SpaceID: 1, IndexID: 1}
		_, err := s.validate()
		if (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	for _, c := range []ddColumn{{Type: 16, Collation: 33, Length: 4}, {Type: 29, Collation: 33, Length: 65536}, {Type: 27, Collation: 28, Length: 65535}, {Type: 22, Collation: 28}} {
		if _, issue, err := metadataColumn(c); err != nil || issue == "" {
			t.Fatal(c, issue, err)
		}
	}
}
