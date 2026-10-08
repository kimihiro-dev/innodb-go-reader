package innodb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func spaceResult(t testing.TB, b []byte) *SpaceReport {
	t.Helper()
	r, err := AnalyzeSpace(context.Background(), bytes.NewReader(b), int64(len(b)), SpaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSpaceDamage(t *testing.T) {
	b := scanFixture(t, "testdata/secondary/empty.ibd.gz")
	good := spaceResult(t, b)
	cases := map[string]func([]byte){
		"FSP size":         func(x []byte) { be.PutUint32(x[46:], ^uint32(0)) },
		"free limit":       func(x []byte) { be.PutUint32(x[50:], 65) },
		"fragment counter": func(x []byte) { be.PutUint32(x[58:], 999) },
		"extent state":     func(x []byte) { be.PutUint32(x[170:], 1) },
		"descriptor free":  func(x []byte) { x[174] |= 1 },
		"extent unlinked": func(x []byte) {
			be.PutUint32(x[78:], 0)
			be.PutUint32(x[82:], ^uint32(0))
			be.PutUint32(x[88:], ^uint32(0))
		},
		"list cycle":        func(x []byte) { be.PutUint32(x[78:], 2); copy(x[164:170], x[82:88]) },
		"inode magic":       func(x []byte) { x[2*PageSize+110] ^= 1 },
		"duplicate segment": func(x []byte) { copy(x[2*PageSize+242:], x[2*PageSize+50:2*PageSize+58]) },
		"root inode":        func(x []byte) { be.PutUint32(x[3*PageSize+78:], ^uint32(0)) },
		"index height":      func(x []byte) { be.PutUint16(x[3*PageSize+64:], 500) },
		"missing root":      func(x []byte) { clear(x[3*PageSize+74 : 3*PageSize+94]) },
		"wrong page number": func(x []byte) { be.PutUint32(x[3*PageSize+4:], 999) },
		"bitmap position":   func(x []byte) { be.PutUint16(x[4*PageSize+24:], 5) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			x := append([]byte{}, b...)
			mutate(x)
			resealTestPages(x)
			r, err := AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{})
			if !errors.Is(err, ErrCorrupt) || r != nil {
				t.Fatal(r, err)
			}
		})
	}
	for _, n := range []uint32{0, 1, 2, 3} {
		x := append([]byte{}, b...)
		clear(x[int(n)*PageSize : int(n+1)*PageSize])
		r, err := AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{})
		if !errors.Is(err, ErrCorrupt) || r != nil {
			t.Fatal("allocated zero", n, err)
		}
	}
	for _, p := range good.Pages {
		if p.Allocation == "free" {
			x := append([]byte{}, b...)
			x[int(p.Number)*PageSize+200] = 1
			r := spaceResult(t, x)
			if r.Pages[p.Number].Allocation != "free" || r.Pages[p.Number].ChecksumVerified {
				t.Fatal("free residue")
			}
			break
		}
	}
	x := append([]byte{}, b...)
	x[3*PageSize+200] ^= 1
	r, err := AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{})
	if !errors.Is(err, ErrCorrupt) || r != nil {
		t.Fatal("CRC", err)
	}
	// Unknown allocated payloads retain bytes and ownership, without claiming a
	// supported payload decoder. A LOB page can have an unknown future type.
	x = scanFixture(t, "testdata/secondary_query/huge.ibd.gz")
	be.PutUint16(x[6*PageSize+24:], 65000)
	resealTestPages(x)
	r = spaceResult(t, x)
	if len(r.Pages[6].Raw) != PageSize || !r.Pages[6].ChecksumVerified || r.Pages[6].SegmentID == 0 {
		t.Fatal("unknown payload")
	}
	r.Pages[6].Raw[200] ^= 1
	if r.Pages[6].Raw[200] == x[6*PageSize+200] {
		t.Fatal("borrowed raw bytes")
	}
}

type spaceFailReader struct {
	r      io.ReaderAt
	n      int
	cancel context.CancelFunc
}

func (r *spaceFailReader) ReadAt(b []byte, off int64) (int, error) {
	r.n++
	if r.cancel != nil && r.n == 2 {
		r.cancel()
	}
	if r.cancel == nil && r.n == 3 {
		return 0, io.ErrClosedPipe
	}
	return r.r.ReadAt(b, off)
}
func TestSpaceControl(t *testing.T) {
	b := scanFixture(t, "testdata/secondary/empty.ibd.gz")
	good := spaceResult(t, b)
	for _, o := range []SpaceOptions{{MaxPages: good.FilePages - 1}, {MaxEntries: 1}} {
		r, err := AnalyzeSpace(context.Background(), bytes.NewReader(b), int64(len(b)), o)
		if !errors.Is(err, ErrLimit) || r != nil {
			t.Fatal("budget", r, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &spaceFailReader{r: bytes.NewReader(b), cancel: cancel}
	r, err := AnalyzeSpace(ctx, f, int64(len(b)), SpaceOptions{})
	if !errors.Is(err, context.Canceled) || r != nil {
		t.Fatal("cancel", err)
	}
	f = &spaceFailReader{r: bytes.NewReader(b)}
	r, err = AnalyzeSpace(context.Background(), f, int64(len(b)), SpaceOptions{})
	if !errors.Is(err, io.ErrClosedPipe) || r != nil {
		t.Fatal("I/O", err)
	}
	for _, size := range []int64{0, 1, int64(len(b)) - 1} {
		r, err = AnalyzeSpace(context.Background(), bytes.NewReader(b), size, SpaceOptions{})
		if !errors.Is(err, ErrCorrupt) || r != nil {
			t.Fatal("size", err)
		}
	}
	r, err = AnalyzeSpace(context.Background(), bytes.NewReader(b[:PageSize]), int64(len(b)), SpaceOptions{})
	if !errors.Is(err, io.EOF) || r != nil {
		t.Fatal("short input", err)
	}
}
func FuzzSpaceAllocation(f *testing.F) {
	b := scanFixture(f, "testdata/secondary/empty.ibd.gz")
	f.Add(uint32(174), byte(1))
	f.Add(uint32(2*PageSize+110), byte(255))
	f.Fuzz(func(t *testing.T, off uint32, value byte) {
		x := append([]byte{}, b...)
		x[int(off)%len(x)] ^= value
		resealTestPages(x)
		r, err := AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{MaxPages: 100, MaxEntries: 2000})
		if err != nil {
			if r != nil {
				t.Fatal("partial report")
			}
			return
		}
		if r.FilePages != r.UsedPages+r.FreePages+r.UninitializedPages+r.TailPages {
			t.Fatal("accounting")
		}
		seen := map[uint32]bool{}
		for _, s := range r.Segments {
			for _, n := range s.FragmentPages {
				if seen[n] {
					t.Fatal("duplicate fragment")
				}
				seen[n] = true
				if r.Pages[n].Allocation != "used" {
					t.Fatal("free fragment")
				}
			}
		}
	})
}

func TestSpaceIdentityBounds(t *testing.T) {
	b := scanFixture(t, "testdata/secondary/empty.ibd.gz")
	// A system-space identity must be rejected before traversing private FSEG data.
	x := append([]byte{}, b...)
	be.PutUint32(x[34:], 0)
	be.PutUint32(x[38:], 0)
	resealTestPages(x)
	r, err := AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{})
	if !errors.Is(err, ErrUnsupported) || r != nil {
		t.Fatal("system-space scope", err)
	}
	x = append([]byte{}, b...)
	be.PutUint64(x[4*PageSize+66:], 0)
	resealTestPages(x)
	r, err = AnalyzeSpace(context.Background(), bytes.NewReader(x), int64(len(x)), SpaceOptions{})
	if !errors.Is(err, ErrCorrupt) || r != nil {
		t.Fatal("zero index ID", err)
	}
}
