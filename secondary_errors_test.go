package innodb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

func secondaryFixture(t testing.TB, name, index string) ([]byte, SecondarySchema) {
	t.Helper()
	b := scanFixture(t, "testdata/secondary/"+name+".ibd.gz")
	s, err := InspectSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		t.Fatal(err)
	}
	return b, *s
}
func TestSecondaryFailures(t *testing.T) {
	b, s := secondaryFixture(t, "deep", "b_idx")
	size := int64(len(b))
	full, err := ReadSecondary(bytes.NewReader(b), size, s)
	if err != nil {
		t.Fatal(err)
	}
	first := full.Records[0]
	root := int(s.RootPage) * PageSize
	cases := map[string]func([]byte){
		"checksum":       func(x []byte) { x[root+200] ^= 1 },
		"index identity": func(x []byte) { x[root+73] ^= 1 },
		"root sibling":   func(x []byte) { be.PutUint32(x[root+8:root+12], 7) },
		"level":          func(x []byte) { be.PutUint16(x[root+64:root+66], 99) },
		"zero child": func(x []byte) {
			n := full.Nodes[0]
			pos := int(n.PageNumber)*PageSize + n.End - 4
			be.PutUint32(x[pos:pos+4], 0)
		},
		"cycle": func(x []byte) {
			n := full.Nodes[0]
			pos := int(n.PageNumber)*PageSize + n.End - 4
			be.PutUint32(x[pos:pos+4], s.RootPage)
		},
		"leaf link":    func(x []byte) { off := int(first.PageNumber) * PageSize; be.PutUint32(x[off+12:off+16], ^uint32(0)) },
		"leaf status":  func(x []byte) { off := int(first.PageNumber)*PageSize + first.Offset; x[off-3] |= 1 },
		"leaf version": func(x []byte) { off := int(first.PageNumber)*PageSize + first.Offset; x[off-5] |= 0x40 },
		"directory":    func(x []byte) { x[root+PageSize-10] ^= 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			x := append([]byte{}, b...)
			mutate(x)
			if name != "checksum" {
				resealTestPages(x)
			}
			got, err := ReadSecondary(bytes.NewReader(x), size, s)
			if err == nil || got != nil {
				t.Fatal("accepted damage", err)
			}
		})
	}
	marker := errors.New("callback failure")
	calls := 0
	report, err := ScanSecondary(context.Background(), bytes.NewReader(b), size, s, ScanOptions{}, func(e SecondaryEvent) error {
		if e.Record != nil {
			calls++
			if calls == 3 {
				return marker
			}
		}
		return nil
	})
	if !errors.Is(err, marker) || report.Complete || report.Records != 3 {
		t.Fatal(report, err)
	}
	for _, options := range []ScanOptions{{MaxRows: 1}, {MaxRowBytes: 1}, {MaxPageReads: 1}, {MaxEntries: 1}} {
		report, err := ScanSecondary(context.Background(), bytes.NewReader(b), size, s, options, func(e SecondaryEvent) error { return nil })
		if !errors.Is(err, ErrLimit) || report.Complete {
			t.Fatal(options, report, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	report, err = ScanSecondary(ctx, bytes.NewReader(b), size, s, ScanOptions{}, func(e SecondaryEvent) error {
		if e.Record != nil {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || report.Complete || report.Records != 1 {
		t.Fatal(report, err)
	}
	report, err = ScanSecondary(context.Background(), bytes.NewReader(b), size, s, ScanOptions{}, func(e SecondaryEvent) error { return ErrStopped })
	if !errors.Is(err, ErrStopped) || report.Complete || report.Pages != 1 {
		t.Fatal(report, err)
	}
	fail := &scanFailReader{ReaderAt: bytes.NewReader(b), fail: 8, err: io.ErrUnexpectedEOF}
	report, err = ScanSecondary(context.Background(), fail, size, s, ScanOptions{}, func(e SecondaryEvent) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) || report.Complete || report.Records == 0 {
		t.Fatal(report, err)
	}
	// Mutating every delivered object must not change tree traversal/order.
	report, err = ScanSecondary(context.Background(), bytes.NewReader(b), size, s, ScanOptions{CachePages: 1}, func(e SecondaryEvent) error {
		if e.Page != nil {
			e.Page.Slots[0] = 0
			e.Page.Next = 0
		}
		var r *SecondaryRecord
		if e.Node != nil {
			r = &e.Node.SecondaryRecord
			e.Node.ChildPage = 0
		}
		if e.Record != nil {
			r = e.Record
		}
		if r != nil {
			for i := range r.Values {
				r.Values[i] = nil
			}
			for _, raw := range r.FieldBytes {
				clear(raw)
			}
			clear(r.Raw)
			for i := range r.ClusteredKey {
				r.ClusteredKey[i] = nil
			}
		}
		return nil
	})
	if err != nil || !report.Complete || report.Records != uint64(len(full.Records)) {
		t.Fatal(report, err)
	}
}
func TestSecondarySchemaValidation(t *testing.T) {
	b, s := secondaryFixture(t, "overlap", "prefix_idx")
	if len(s.Fields) != 3 || s.Fields[0].Column != s.Fields[1].Column || s.Fields[0].PrefixBytes != 2 || !reflect.DeepEqual(s.ClusteredFields, []int{1, 2}) {
		t.Fatal("prefix/full overlap", s)
	}
	cases := map[string]func(*SecondarySchema){"name": func(s *SecondarySchema) { s.Name = "" }, "root": func(s *SecondarySchema) { s.RootPage = 0 }, "users": func(s *SecondarySchema) { s.UserFields = 0 }, "locator": func(s *SecondarySchema) { s.ClusteredFields = []int{0} }, "duplicate locator": func(s *SecondarySchema) { s.ClusteredFields = []int{1, 1} }, "range": func(s *SecondarySchema) { s.ClusteredFields = []int{99} }, "width": func(s *SecondarySchema) { s.Fields[0].PrefixBytes = 9999 }, "type": func(s *SecondarySchema) { s.Fields[0].Definition.Type = "FLOAT" }, "charset": func(s *SecondarySchema) { s.Fields[0].Definition.Charset = "unknown" }, "nullable locator": func(s *SecondarySchema) { s.Fields[1].Definition.Nullable = true }, "rowid": func(s *SecondarySchema) { s.Fields[0].RowID = true }, "format": func(s *SecondarySchema) { s.RowFormat = "REDUNDANT" }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := s
			v.Fields = append([]SecondaryField{}, s.Fields...)
			v.ClusteredFields = append([]int{}, s.ClusteredFields...)
			mutate(&v)
			got, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), v)
			if got != nil || !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"absent", "PRIMARY", ""} {
		if _, err := InspectSecondary(bytes.NewReader(b), int64(len(b)), name); !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
	}
	if _, err := ScanSecondary(nil, bytes.NewReader(b), int64(len(b)), s, ScanOptions{}, func(SecondaryEvent) error { return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := ScanSecondary(context.Background(), bytes.NewReader(b), int64(len(b)), s, ScanOptions{}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
func TestSecondaryUniqueAndPhysicalOrder(t *testing.T) {
	b, s := secondaryFixture(t, "tiny", "n_idx")
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	var a, z SecondaryRecord
	for i := 1; i < len(full.Records); i++ {
		if full.Records[i-1].Values[0] != nil && full.Records[i].Values[0] != nil {
			a = full.Records[i-1]
			z = full.Records[i]
			break
		}
	}
	off := int(z.PageNumber)*PageSize + z.Offset
	from := int(a.PageNumber)*PageSize + a.Offset
	copy(b[off:off+2], b[from:from+2])
	resealTestPages(b)
	if got, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), s); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("physical duplicate", err)
	}
	b, s = secondaryFixture(t, "tiny", "n_idx")
	s.Unique = true
	if got, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), s); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("unique duplicate", err)
	}
}
func FuzzSecondaryPage(f *testing.F) {
	b, s := secondaryFixture(f, "tiny", "n_idx")
	f.Add(uint16(130), byte(1))
	f.Add(uint16(54), byte(255))
	f.Fuzz(func(t *testing.T, offset uint16, value byte) {
		x := append([]byte{}, b...)
		off := int(s.RootPage)*PageSize + int(offset)%PageSize
		x[off] ^= value
		resealTestPages(x)
		result, err := ReadSecondary(bytes.NewReader(x), int64(len(x)), s)
		if err != nil && result != nil {
			t.Fatal("partial result")
		}
	})
}
