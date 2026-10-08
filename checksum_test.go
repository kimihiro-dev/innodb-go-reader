package innodb

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// resealTestPages is only for structural mutations: retain a valid envelope so
// the original record/tree/LOB checks are reached. Never use in checksum tests.
func resealTestPages(b []byte) {
	for off := 0; off+PageSize <= len(b); off += PageSize {
		p := b[off : off+PageSize]
		if bytes.Count(p, []byte{0}) == PageSize {
			continue
		}
		copy(p[PageSize-4:], p[20:24])
		c := pageCRC(p)
		be.PutUint32(p[:4], c)
		be.PutUint32(p[PageSize-8:], c)
	}
}

// Bitwise oracle deliberately does not use the production lookup table.
func bitwiseCRC(b []byte) uint32 {
	c := ^uint32(0)
	for _, v := range b {
		c ^= uint32(v)
		for i := 0; i < 8; i++ {
			if c&1 != 0 {
				c = c>>1 ^ 0x82f63b78
			} else {
				c >>= 1
			}
		}
	}
	return ^c
}

func TestChecksumRealPages(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	want := []uint32{0x418a386e, 0x5215f894, 0x67b8063f, 0x5b18dbeb, 0xbcf5b404}
	for i, w := range want {
		p := b[i*PageSize : (i+1)*PageSize]
		if pageCRC(p) != w || bitwiseCRC(p[4:26])^bitwiseCRC(p[38:PageSize-8]) != w {
			t.Fatal("CRC oracle", i)
		}
		if _, err := readPage(bytes.NewReader(b), int64(len(b)), uint32(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Unreferenced zero capacity pages are present but do not prevent Read.
	if _, err := Read(bytes.NewReader(b), int64(len(b)), s); err != nil {
		t.Fatal(err)
	}
	if _, err := readPage(bytes.NewReader(b), int64(len(b)), 5); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "all zero") {
		t.Fatal(err)
	}
}

func TestChecksumDamage(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	base := int(s.RootPage) * PageSize
	for _, off := range []int{0, 3, 4, 16, 24, 25, 38, 176, PageSize - 9, PageSize - 8, PageSize - 5, PageSize - 4, PageSize - 1} {
		t.Run(fmt.Sprint(off), func(t *testing.T) {
			bad := append([]byte(nil), b...)
			bad[base+off] ^= 1
			r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if r != nil || !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "page 4 at file offset 65536") {
				t.Fatal(err)
			}
		})
	}
	for _, field := range []uint32{0, 0xdeadbeef, 0x12345678} {
		bad := append([]byte(nil), b...)
		be.PutUint32(bad[base:], field)
		be.PutUint32(bad[base+PageSize-8:], field)
		if r, err := Read(bytes.NewReader(bad), int64(len(bad)), s); r != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
	}
	// Even a matching recalculated CRC cannot hide a stale trailer LSN.
	stale := append([]byte(nil), b...)
	page := stale[base : base+PageSize]
	page[20] ^= 1
	c := pageCRC(page)
	be.PutUint32(page[:4], c)
	be.PutUint32(page[PageSize-8:], c)
	if r, err := Read(bytes.NewReader(stale), int64(len(stale)), s); r != nil || !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "LSN") {
		t.Fatal(err)
	}
	// Read checks the selected tree, not unrelated SDI pages.
	unrelated := append([]byte(nil), b...)
	unrelated[3*PageSize+100] ^= 1
	if _, err := Read(bytes.NewReader(unrelated), int64(len(unrelated)), s); err != nil {
		t.Fatal(err)
	}
	// CRC deliberately excludes [26,38); semantic space-ID validation is separate.
	p := append([]byte(nil), b[base:base+PageSize]...)
	for off := 26; off < 38; off++ {
		p[off] ^= 1
	}
	if err := verifyPageChecksum(p); err != nil {
		t.Fatal("excluded bytes", err)
	}
	bad := append([]byte(nil), b...)
	bad[base+34] ^= 1
	if r, err := Read(bytes.NewReader(bad), int64(len(bad)), s); r != nil || !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "space/index ID") {
		t.Fatal(err)
	}
}

func TestChecksumAllReadPaths(t *testing.T) {
	for _, name := range []string{"root", "leaf", "lob-first", "lob-data", "lob-index", "fsp"} {
		t.Run(name, func(t *testing.T) {
			var b []byte
			var s Schema
			var page uint32
			switch name {
			case "leaf":
				b, s = treeFixture(t, "ordered_rows")
				r, err := Read(bytes.NewReader(b), int64(len(b)), s)
				if err != nil {
					t.Fatal(err)
				}
				page = r.Records[len(r.Records)-1].PageNumber
			case "lob-first", "lob-data", "lob-index":
				b, s = largeLOBFixture(t, "medium_index")
				// Pick a page by type; the integration assertion below ensures it is read.
				typ := uint16(24)
				if name == "lob-data" {
					typ = 23
				}
				if name == "lob-index" {
					typ = 22
				}
				for i := 0; i < len(b)/PageSize; i++ {
					if be.Uint16(b[i*PageSize+24:]) == typ {
						page = uint32(i)
						break
					}
				}
				if page == 0 {
					t.Fatal("missing page type", typ)
				}
			default:
				b, s = fixture(t, "lesson_rows")
				if name == "root" {
					page = s.RootPage
				}
			}
			bad := append([]byte(nil), b...)
			bad[int(page)*PageSize+100] ^= 1
			r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if r != nil || !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "CRC32C mismatch") {
				t.Fatal("not checked", err)
			}
		})
	}
}

func FuzzChecksum(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(uint16(176), byte(1))
	f.Fuzz(func(t *testing.T, off uint16, mask byte) {
		bad := append([]byte(nil), b...)
		bad[int(s.RootPage)*PageSize+int(off)%PageSize] ^= mask
		r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && r != nil {
			t.Fatal("partial")
		}
	})
}
