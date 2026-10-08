package innodb

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSDIFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/sdi/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Source, Expected, SHA256 string
			Records                  int
		}
	}
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 158 {
		t.Fatal("asset count")
	}
	count, external, maxChunks := 0, 0, 0
	for _, c := range m.Cases {
		t.Run(c.Source, func(t *testing.T) {
			var b []byte
			if filepath.Ext(c.Source) == ".gz" {
				b = unzip(t, c.Source)
			} else {
				b, err = os.ReadFile(c.Source)
				if err != nil {
					t.Fatal(err)
				}
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			result, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var want []json.RawMessage
			if err = json.Unmarshal(unzip(t, filepath.Join("testdata/sdi", c.Expected)), &want); err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != c.Records || len(want) != c.Records+1 {
				t.Fatal("count")
			}
			for i, r := range result.Records {
				var item struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(want[i+1], &item); err != nil {
					t.Fatal(err)
				}
				decode := func(b []byte) any {
					var v any
					d := json.NewDecoder(bytes.NewReader(b))
					d.UseNumber()
					if err := d.Decode(&v); err != nil {
						t.Fatal(err)
					}
					return v
				}
				if r.Key != (SDIKey{item.Type, item.ID}) || !reflect.DeepEqual(decode(r.JSON), decode(item.Object)) {
					t.Fatal("official SDI mismatch", i)
				}
				if len(r.JSON) != int(r.UncompressedLength) {
					t.Fatal("length")
				}
				if r.External != nil {
					external++
					n := r.External.PrefixLength
					for _, ch := range r.External.Chunks {
						n += ch.Length
						if ch.Offset != 46 {
							t.Fatal("offset")
						}
					}
					if n != int(r.CompressedLength) {
						t.Fatal("external length")
					}
					if len(r.External.Chunks) > maxChunks {
						maxChunks = len(r.External.Chunks)
					}
				}
			}
			count += len(result.Records)
		})
	}
	if count != 316 || external == 0 || maxChunks < 29 {
		t.Fatal("coverage", count, external, maxChunks)
	}
	t.Log("objects", count, "external", external, "max chunks", maxChunks)
}

func TestSDIDamage(t *testing.T) {
	b, _ := fixture(t, "lesson_rows")
	r, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[0]
	base := int(rec.PageNumber) * PageSize
	o := base + rec.Offset
	cases := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"flag", func(b []byte) { be.PutUint32(b[54:], be.Uint32(b[54:])&^0x4000) }, ErrUnsupported},
		{"version", func(b []byte) { be.PutUint32(b[sdiHeaderOffset:], 2) }, ErrUnsupported},
		{"zero-root", func(b []byte) { be.PutUint32(b[sdiHeaderOffset+4:], 0) }, ErrCorrupt},
		{"far-root", func(b []byte) { be.PutUint32(b[sdiHeaderOffset+4:], 9999) }, ErrCorrupt},
		{"type", func(b []byte) { be.PutUint16(b[base+24:], 17855) }, ErrUnsupported},
		{"space", func(b []byte) { be.PutUint32(b[base+34:], 999) }, ErrCorrupt},
		{"deleted", func(b []byte) { b[o-5] |= 0x20 }, ErrUnsupported},
		{"length", func(b []byte) { be.PutUint32(b[o+29:], 1) }, ErrCorrupt},
		{"zero-length", func(b []byte) { be.PutUint32(b[o+25:], 0) }, ErrCorrupt},
		{"limit", func(b []byte) { be.PutUint32(b[o+25:], MaxSDIObjectBytes+1) }, ErrUnsupported},
		{"zlib", func(b []byte) { b[o+33] = 0 }, ErrCorrupt},
		{"inflated-length", func(b []byte) { be.PutUint32(b[o+25:], rec.UncompressedLength+1) }, ErrCorrupt},
		{"next-cycle", func(b []byte) { be.PutUint16(b[o-2:], PageSize) }, ErrCorrupt},
		{"level", func(b []byte) { be.PutUint16(b[base+64:], 0xffff) }, ErrCorrupt},
		{"sibling", func(b []byte) { be.PutUint32(b[base+12:], 999) }, ErrCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			c.mutate(bad)
			resealTestPages(bad)
			got, err := ReadSDI(bytes.NewReader(bad), int64(len(bad)))
			if got != nil || !errors.Is(err, c.want) {
				t.Fatal(err)
			}
		})
	}
	bad := append([]byte(nil), b...)
	bad[o+33] ^= 1
	if got, err := ReadSDI(bytes.NewReader(bad), int64(len(bad))); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

func TestSDIBlobDamage(t *testing.T) {
	b, _ := enumFixture(t, "enum_65535")
	r, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[0]
	ref := int(rec.PageNumber)*PageSize + rec.External.Offset
	page := int(rec.External.Chunks[0].PageNumber) * PageSize
	for _, c := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"space", func(b []byte) { be.PutUint32(b[ref:], 0) }},
		{"offset", func(b []byte) { be.PutUint32(b[ref+8:], 1) }},
		{"reference-length", func(b []byte) { be.PutUint64(b[ref+12:], 0xffffffffffffffff) }},
		{"zero-start", func(b []byte) { be.PutUint32(b[ref+4:], 0) }},
		{"cycle", func(b []byte) { be.PutUint32(b[page+42:], uint32(page/PageSize)) }},
		{"short", func(b []byte) { be.PutUint32(b[page+42:], ^uint32(0)) }},
		{"part-zero", func(b []byte) { be.PutUint32(b[page+38:], 0) }},
		{"part-long", func(b []byte) { be.PutUint32(b[page+38:], PageSize) }},
		{"blob-space", func(b []byte) { be.PutUint32(b[page+34:], 0) }},
		{"blob-type", func(b []byte) { be.PutUint16(b[page+24:], 19) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			c.mutate(bad)
			resealTestPages(bad)
			got, err := ReadSDI(bytes.NewReader(bad), int64(len(bad)))
			if got != nil || err == nil {
				t.Fatal("accepted", err)
			}
		})
	}
}

func compressSDITest(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	if _, err := z.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func TestSDIInflate(t *testing.T) {
	original := []byte(" {\"n\":18446744073709551615,\"s\":\"界\"}\n")
	c := compressSDITest(t, original)
	got, err := inflateSDI(c, uint32(len(original)))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(err)
	}
	for _, test := range []struct {
		data []byte
		n    uint32
	}{
		{append(append([]byte(nil), c...), 0), uint32(len(original))},
		{c[:len(c)-1], uint32(len(original))}, {c, uint32(len(original) - 1)}, {c, uint32(len(original) + 1)},
		{compressSDITest(t, []byte("[]")), 2}, {compressSDITest(t, []byte("{]")), 2},
		{compressSDITest(t, []byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}), 9},
		{c, MaxSDIObjectBytes + 1},
	} {
		if got, err := inflateSDI(test.data, test.n); got != nil || err == nil {
			t.Fatal("accepted malformed")
		}
	}
}

// Build a synthetic two-leaf SDI tree from real, unmodified record bodies.
// This is structural coverage, not a claim that these pages came from MySQL.
func sdiTwoLeaf(t testing.TB) ([]byte, *SDIResult) {
	t.Helper()
	b, _ := fixture(t, "lesson_rows")
	original, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	source := append([]byte(nil), b[3*PageSize:4*PageSize]...)
	b = append(b, make([]byte, 2*PageSize)...)
	makePage := func(number uint32, level uint16, prev, next uint32, bodies [][]byte, leaf bool) {
		p := b[int(number)*PageSize : int(number+1)*PageSize]
		copy(p, source)
		clear(p[120 : PageSize-8])
		be.PutUint32(p[4:], number)
		be.PutUint32(p[8:], prev)
		be.PutUint32(p[12:], next)
		be.PutUint16(p[64:], level)
		be.PutUint16(p[38:], 2)
		be.PutUint16(p[42:], 0x8000|uint16(len(bodies)+2))
		be.PutUint16(p[44:], 0)
		be.PutUint16(p[46:], 0)
		be.PutUint16(p[54:], uint16(len(bodies)))
		origins := []int{}
		pos := 120
		for i, body := range bodies {
			copy(p[pos:], body)
			h := 5
			if leaf {
				h = 7
			} // Both original lesson_rows records have two-byte length metadata.
			origin := pos + h
			origins = append(origins, origin)
			p[origin-5] = 0
			if !leaf && i == 0 {
				p[origin-5] = 0x10
			}
			status := uint16(0)
			if !leaf {
				status = 1
			}
			be.PutUint16(p[origin-4:], uint16(i+2)<<3|status)
			pos += len(body)
		}
		for i, o := range origins {
			next := supremum
			if i+1 < len(origins) {
				next = origins[i+1]
			}
			be.PutUint16(p[o-2:], uint16((next-o)&(PageSize-1)))
		}
		be.PutUint16(p[97:], uint16(origins[0]-99))
		p[107] = byte(len(origins) + 1)
		be.PutUint16(p[PageSize-10:], infimum)
		be.PutUint16(p[PageSize-12:], supremum)
		be.PutUint16(p[40:], uint16(pos))
	}
	for i, rec := range original.Records {
		makePage(uint32(7+i), 0, []uint32{^uint32(0), 7}[i], []uint32{8, ^uint32(0)}[i], [][]byte{source[rec.Start:rec.End]}, true)
	}
	nodes := [][]byte{}
	for i, rec := range original.Records {
		node := make([]byte, 21)
		be.PutUint32(node[5:], rec.Key.Type)
		be.PutUint64(node[9:], rec.Key.ID)
		be.PutUint32(node[17:], uint32(7+i))
		nodes = append(nodes, node)
	}
	makePage(3, 1, ^uint32(0), ^uint32(0), nodes, false)
	resealTestPages(b)
	return b, original
}

func TestSDITree(t *testing.T) {
	b, want := sdiTwoLeaf(t)
	got, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pages) != 3 || len(got.Records) != 2 {
		t.Fatal("tree count")
	}
	for i, r := range got.Records {
		if r.Key != want.Records[i].Key || !bytes.Equal(r.JSON, want.Records[i].JSON) {
			t.Fatal("tree data")
		}
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { be.PutUint32(b[3*PageSize+137:], 3) },
		func(b []byte) { be.PutUint16(b[7*PageSize+64:], 1) },
		func(b []byte) { be.PutUint32(b[8*PageSize+8:], 0) },
		func(b []byte) { be.PutUint64(b[3*PageSize+150:], ^uint64(0)) },
	} {
		bad := append([]byte(nil), b...)
		mutate(bad)
		resealTestPages(bad)
		if got, err := ReadSDI(bytes.NewReader(bad), int64(len(bad))); got != nil || err == nil {
			t.Fatal("accepted bad tree")
		}
	}
	// Root discovery is from page 0, not hard-coded page 3.
	relocated := append([]byte(nil), b...)
	page := append([]byte(nil), b[3*PageSize:4*PageSize]...)
	be.PutUint32(page[4:], 9)
	relocated = append(relocated, page...)
	be.PutUint32(relocated[sdiHeaderOffset+4:], 9)
	resealTestPages(relocated)
	if got, err := ReadSDI(bytes.NewReader(relocated), int64(len(relocated))); err != nil || got.RootPage != 9 {
		t.Fatal(err)
	}
}

func FuzzSDI(f *testing.F) {
	b, _ := fixture(f, "lesson_rows")
	f.Add(uint32(3*PageSize+477), []byte{255})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		got, err := ReadSDI(bytes.NewReader(bad), int64(len(bad)))
		if err != nil && got != nil {
			t.Fatal("partial")
		}
	})
}

func TestSDIInputAndEmptyRoot(t *testing.T) {
	b, _ := fixture(t, "lesson_rows")
	for _, size := range []int64{0, 1, int64(len(b) - 1), PageSize} {
		if r, err := ReadSDI(bytes.NewReader(b), size); r != nil || err == nil {
			t.Fatal("input", err)
		}
	}
	bad := append([]byte(nil), b...)
	be.PutUint32(bad[34:], 0)
	resealTestPages(bad)
	if r, err := ReadSDI(bytes.NewReader(bad), int64(len(bad))); r != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	p := b[3*PageSize : 4*PageSize]
	clear(p[120 : PageSize-8])
	be.PutUint16(p[40:], 120)
	be.PutUint16(p[42:], 0x8002)
	be.PutUint16(p[44:], 0)
	be.PutUint16(p[46:], 0)
	be.PutUint16(p[54:], 0)
	be.PutUint16(p[38:], 2)
	be.PutUint16(p[97:], 13)
	p[107] = 1
	be.PutUint16(p[PageSize-10:], infimum)
	be.PutUint16(p[PageSize-12:], supremum)
	resealTestPages(b)
	r, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil || r.Records == nil || len(r.Records) != 0 {
		t.Fatal("empty root", err)
	}
}

func TestSDIPrefixAndShortLength(t *testing.T) {
	// Synthetic single SDI_BLOB page, with a 768-byte inline prefix.
	b, _ := enumFixture(t, "enum_65535")
	p := b[5*PageSize : 6*PageSize]
	local := bytes.Repeat([]byte{'x'}, 788)
	ref := local[768:]
	clear(ref)
	be.PutUint32(ref, 151)
	be.PutUint32(ref[4:], 5)
	be.PutUint32(ref[8:], 38)
	be.PutUint64(ref[12:], 2)
	be.PutUint32(p[38:], 2)
	be.PutUint32(p[42:], ^uint32(0))
	copy(p[46:], "yz")
	resealTestPages(b)
	got, ext, err := readSDIBlob(bytes.NewReader(b), int64(len(b)), 151, local, 100, 770)
	if err != nil || len(got) != 770 || string(got[768:]) != "yz" || ext.Offset != 868 || ext.PrefixLength != 768 {
		t.Fatal(err)
	}
	if _, _, err := readSDIBlob(bytes.NewReader(b), int64(len(b)), 151, local[:19], 100, 770); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	// A short compressed object uses one metadata byte. This is a record decoder test.
	payload := compressSDITest(t, []byte("{}"))
	page := make([]byte, PageSize)
	origin := 126
	page[origin-6] = byte(len(payload))
	be.PutUint16(page[origin-4:], 2<<3)
	be.PutUint32(page[origin:], 1)
	be.PutUint64(page[origin+4:], ^uint64(0))
	be.PutUint32(page[origin+25:], 2)
	be.PutUint32(page[origin+29:], uint32(len(payload)))
	copy(page[origin+33:], payload)
	e, err := decodeSDIEntry(page, origin, origin+33+len(payload), 0)
	if err != nil || e.Start != 120 || e.sdiKey.ID != ^uint64(0) {
		t.Fatal(err)
	}
	if !(SDIKey{1, ^uint64(0)}).less(SDIKey{2, 0}) || (SDIKey{1, 2}).less(SDIKey{1, 1}) {
		t.Fatal("unsigned tuple order")
	}
}
