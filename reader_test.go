package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const fixtures = "testdata/mysql8045"

func fixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name+".ibd"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := os.ReadFile(filepath.Join(fixtures, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err := json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestMySQLFixtures(t *testing.T) {
	m, err := os.ReadFile(filepath.Join(fixtures, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err := json.Unmarshal(m, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := fixture(t, c.Name)
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != c.SHA256 {
				t.Fatal("fixture SHA256 mismatch")
			}
			got, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			rows := make([][]any, len(got.Records))
			for i, r := range got.Records {
				rows[i] = r.Values
				root := int(s.RootPage) * PageSize
				if !bytes.Equal(r.Transaction[:], b[root+r.Offset+4:root+r.Offset+10]) ||
					!bytes.Equal(r.RollPointer[:], b[root+r.Offset+10:root+r.Offset+17]) {
					t.Fatal("system field bytes lost")
				}
			}
			encoded, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(fixtures, c.Name+".expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var a, z any
			if err := json.Unmarshal(encoded, &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, &z); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, z) || len(rows) != c.Rows {
				t.Fatalf("rows differ from SQL snapshot\ngot %s\nwant %s", encoded, want)
			}
		})
	}
}

func TestRecordLocations(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	var offsets []int
	for _, rec := range r.Records {
		offsets = append(offsets, rec.Offset)
	}
	if !reflect.DeepEqual(offsets, []int{155, 222, 127, 189}) {
		t.Fatalf("unexpected logical record chain %v", offsets)
	}
	if r.Records[1].NextOffset >= r.Records[1].Offset {
		t.Fatal("fixture must exercise backward link")
	}
}

func TestRootIsExplicit(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	root := append([]byte(nil), b[int(s.RootPage)*PageSize:int(s.RootPage+1)*PageSize]...)
	s.RootPage = uint32(len(b) / PageSize)
	be.PutUint32(root[4:8], s.RootPage)
	b = append(b, root...)
	resealTestPages(b)
	if _, err := Read(bytes.NewReader(b), int64(len(b)), s); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedPage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte, []byte)
		want   error
	}{
		{"wrong-space", func(b, p []byte) { be.PutUint32(p[34:38], 999) }, ErrCorrupt},
		{"wrong-index", func(b, p []byte) { be.PutUint64(p[66:74], 999) }, ErrCorrupt},
		{"wrong-page-number", func(b, p []byte) { be.PutUint32(p[4:8], 999) }, ErrCorrupt},
		{"wrong-type", func(b, p []byte) { be.PutUint16(p[24:26], 17853) }, ErrUnsupported},
		{"non-leaf", func(b, p []byte) { be.PutUint16(p[64:66], 1) }, ErrCorrupt},
		{"sibling", func(b, p []byte) { be.PutUint32(p[12:16], 7) }, ErrCorrupt},
		{"redundant", func(b, p []byte) { p[42] &= 0x7f }, ErrUnsupported},
		{"garbage", func(b, p []byte) { p[47] = 1 }, ErrCorrupt},
		{"compressed", func(b, p []byte) { b[57] |= 2 }, ErrUnsupported},
		{"encrypted", func(b, p []byte) { b[56] |= 0x20 }, ErrUnsupported},
		{"shared", func(b, p []byte) { b[56] |= 8 }, ErrUnsupported},
		{"other-page-size", func(b, p []byte) { b[57] |= 0xc0 }, ErrUnsupported},
		{"short-heap", func(b, p []byte) { be.PutUint16(p[40:42], 100) }, ErrCorrupt},
		{"directory-overlap", func(b, p []byte) { be.PutUint16(p[40:42], 16380) }, ErrCorrupt},
		{"heap-count", func(b, p []byte) { be.PutUint16(p[42:44], 0x8002) }, ErrCorrupt},
		{"slot-count", func(b, p []byte) { be.PutUint16(p[38:40], 0xffff) }, ErrCorrupt},
		{"bad-slot", func(b, p []byte) { be.PutUint16(p[PageSize-12:PageSize-10], 123) }, ErrCorrupt},
		{"bad-system-record", func(b, p []byte) { p[99] = 0 }, ErrCorrupt},
		{"out-of-heap-link", func(b, p []byte) { be.PutUint16(p[97:99], 15000) }, ErrCorrupt},
		{"zero-link", func(b, p []byte) { be.PutUint16(p[153:155], 0) }, ErrCorrupt},
		{"cycle", func(b, p []byte) { be.PutUint16(p[220:222], uint16(65536+155-222)) }, ErrCorrupt},
		{"early-end", func(b, p []byte) { be.PutUint16(p[97:99], 13) }, ErrCorrupt},
		{"instant", func(b, p []byte) { p[150] |= 0x80 }, ErrUnsupported},
		{"instant", func(b, p []byte) { p[150] |= 0x80 }, ErrUnsupported},
		{"row-version", func(b, p []byte) { p[150] |= 0x40 }, ErrUnsupported},
		{"node-record", func(b, p []byte) { p[152] |= 1 }, ErrUnsupported},
		{"duplicate-heap", func(b, p []byte) { be.PutUint16(p[151:153], 2<<3) }, ErrCorrupt},
		{"oversized-string", func(b, p []byte) { p[148] = 255 }, ErrCorrupt},
		{"overlap", func(b, p []byte) { p[148] = 9 }, ErrCorrupt},
		{"invalid-utf8", func(b, p []byte) { p[176] = 0xff }, ErrCorrupt},
		{"directory-owned", func(b, p []byte) { p[107] = 4 }, ErrCorrupt},
		{"key-order", func(b, p []byte) { be.PutUint32(p[155:159], 0x8000007f) }, ErrCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, s := fixture(t, "lesson_rows")
			p := b[int(s.RootPage)*PageSize : int(s.RootPage+1)*PageSize]
			tc.mutate(b, p)
			resealTestPages(b)
			result, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if !errors.Is(err, tc.want) || result != nil {
				t.Fatalf("got %v, want %v and no partial result", err, tc.want)
			}
		})
	}
}

func TestInputContract(t *testing.T) {
	for _, name := range []string{"missing-pk", "nullable-pk", "wrong-type", "long-varchar", "duplicate", "missing-root", "missing-index"} {
		t.Run(name, func(t *testing.T) {
			b, s := fixture(t, "lesson_rows")
			switch name {
			case "missing-pk":
				s.PrimaryKey = "absent"
			case "nullable-pk":
				s.Columns[0].Nullable = true
			case "wrong-type":
				s.Columns[1].Type = "DECIMAL"
			case "long-varchar":
				s.Columns[2].MaxChars = 16384
			case "duplicate":
				s.Columns[1].Name = "id"
			case "missing-root":
				s.RootPage = 0
			case "missing-index":
				s.IndexID = 0
			}
			if _, err := Read(bytes.NewReader(b), int64(len(b)), s); !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
	b, s := fixture(t, "lesson_rows")
	for _, size := range []int64{0, 1, int64(len(b) - 1), PageSize} {
		if _, err := Read(bytes.NewReader(b), size, s); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	if _, err := Read(bytes.NewReader(b[:100]), int64(len(b)), s); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if _, err := Read(shortReader{}, int64(len(b)), s); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

type shortReader struct{}

func (shortReader) ReadAt(b []byte, offset int64) (int, error) { return 0, nil }

// Mutate arbitrary bytes in a valid root (up to a page); parsing must terminate without panic.
func FuzzRead(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(uint16(150), []byte{0xff, 0xff, 0xff})
	f.Add(uint16(38), []byte{0xff, 0xff})
	f.Add(uint16(97), []byte{0, 0})
	f.Fuzz(func(t *testing.T, offset uint16, changes []byte) {
		data := append([]byte(nil), b...)
		start := int(offset) % PageSize
		p := data[int(s.RootPage)*PageSize : int(s.RootPage+1)*PageSize]
		copy(p[start:], changes)
		resealTestPages(data)
		_, _ = Read(bytes.NewReader(data), int64(len(data)), s)
	})
}
