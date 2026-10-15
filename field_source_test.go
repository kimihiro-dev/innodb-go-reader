package innodb

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestFieldSourceLesson(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r := &localPageReader{data: b}
	p, err := TraceClusteredPage(r, int64(len(b)), 4, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.pages, []uint32{0, 4}) || len(p.Sources) != 4 {
		t.Fatal("trace scope", r.pages)
	}
	source := p.Sources[0]
	if source.Offset != 155 || source.Node || source.DeleteMarked || len(source.Fields) != 5 {
		t.Fatal(source)
	}
	// Independent byte positions documented in chapters 03/04, not derived
	// by re-encoding values or comparing two parser outputs.
	for i, want := range []ByteSpan{{155, 159}, {159, 165}, {165, 172}, {172, 176}, {176, 182}} {
		field := source.Fields[i]
		if field.Position != i || field.Data == nil || *field.Data != want {
			t.Fatal("physical payload position", i, field)
		}
	}
	name := source.Fields[4]
	if name.Column != 2 || name.Name != "name" || name.Kind != "stored" || name.Null || *name.Length != (ByteSpan{148, 149}) || *name.NullBit != (NullBit{149, 2}) {
		t.Fatal("length and NULL metadata", name)
	}
	if got := b[4*PageSize+name.Data.Start : 4*PageSize+name.Data.End]; string(got) != "InnoDB" {
		t.Fatal("raw text bytes")
	}
	for _, field := range p.Sources[1].Fields[3:] {
		if !field.Null || field.Data != nil || field.Length != nil || field.NullBit == nil {
			t.Fatal("NULL cannot have fabricated payload/length", field)
		}
	}
	empty := p.Sources[2].Fields[4]
	if empty.Null || empty.Data == nil || *empty.Data != (ByteSpan{148, 148}) || *empty.Length != (ByteSpan{120, 121}) {
		t.Fatal("empty text differs from NULL", empty)
	}
	plain, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), 4, s)
	if err != nil {
		t.Fatal(err)
	}
	p.Sources = nil
	if !reflect.DeepEqual(p, plain) {
		t.Fatal("source capture changed local values or original results")
	}
}

// Check physical partitioning independently of the decoded values: every payload
// byte belongs to exactly one field, and metadata never overlaps the payload.
func checkRecordSource(t *testing.T, raw []byte, source RecordSource, start, end int) {
	t.Helper()
	pos := source.Offset
	metadata := map[int]byte{}
	mark := func(span *ByteSpan) {
		t.Helper()
		if span == nil {
			return
		}
		if span.Start < start || span.End <= span.Start || span.End > source.Offset-5 {
			t.Fatal("metadata bounds", source.Offset, span)
		}
		for i := span.Start; i < span.End; i++ {
			if metadata[i] != 0 {
				t.Fatal("overlapping metadata", i)
			}
			metadata[i] = 255
		}
	}
	mark(source.RowVersion)
	mark(source.FieldCount)
	ordinal := 0
	for _, f := range source.Fields {
		if f.Kind == "default" {
			if f.Position != -1 || f.Column < 0 || f.Default == nil || f.Null != f.Default.Null || f.Data != nil || f.Length != nil || f.NullBit != nil || f.Reference != nil || f.Prefix != nil {
				t.Fatal("default has physical storage", f)
			}
			continue
		}
		if f.Position != ordinal || f.Default != nil || (f.Kind == "dropped" || f.Kind == "system") != (f.Column == -1) {
			t.Fatal("physical ordinal or column identity", f)
		}
		ordinal++
		mark(f.Length)
		if f.NullBit != nil {
			n := f.NullBit
			if n.Offset < start || n.Offset >= source.Offset-5 || n.Mask == 0 || n.Mask&(n.Mask-1) != 0 || metadata[n.Offset]&n.Mask != 0 {
				t.Fatal("NULL bit bounds/overlap", f)
			}
			metadata[n.Offset] |= n.Mask
			if (raw[n.Offset]&n.Mask != 0) != f.Null {
				t.Fatal("NULL bit value", f)
			}
		}
		if f.Null {
			if f.Data != nil || f.Length != nil || f.NullBit == nil {
				t.Fatal("NULL payload", f)
			}
			continue
		}
		if f.Data == nil || f.Data.Start != pos || f.Data.End < pos || f.Data.End > end {
			t.Fatal("payload partition", f, pos, end)
		}
		pos = f.Data.End
		if f.Reference != nil {
			if f.Reference.Start < f.Data.Start || f.Reference.End != f.Data.End || f.Reference.End-f.Reference.Start != 20 {
				t.Fatal("reference subrange", f)
			}
			if f.Prefix != nil && (f.Prefix.Start != f.Data.Start || f.Prefix.End != f.Reference.Start) {
				t.Fatal("prefix subrange", f)
			}
		}
	}
	if pos != end {
		t.Fatal("unattributed payload", pos, end)
	}
}

func checkClusteredSources(t *testing.T, b []byte, p *ClusteredPage, s Schema) {
	t.Helper()
	if len(p.Sources) != int(p.Page.Records) || !reflect.DeepEqual(p.VirtualColumns, s.VirtualColumns) {
		t.Fatal("missing record/virtual sources")
	}
	raw := b[int(p.Page.Number)*PageSize : int(p.Page.Number+1)*PageSize]
	for _, source := range p.Sources {
		var start, end int
		var record *LocalRecord
		for i := range p.Records {
			if p.Records[i].Offset == source.Offset {
				record = &p.Records[i]
				start, end = record.Start, record.End
			}
		}
		for _, r := range p.DeletedRecords {
			if r.Offset == source.Offset {
				start, end = r.Start, r.End
				if !source.DeleteMarked {
					t.Fatal("delete source flag")
				}
			}
		}
		for _, n := range p.Nodes {
			if n.Offset == source.Offset {
				start, end = n.Start, n.End
			}
		}
		if start == 0 || source.Node != (p.Page.Level > 0) {
			t.Fatal("unmatched source")
		}
		checkRecordSource(t, raw, source, start, end)
		columns := map[int]bool{}
		for _, f := range source.Fields {
			if f.Column >= 0 {
				if f.Column >= len(s.Columns) || f.Name != s.Columns[f.Column].Name || columns[f.Column] {
					t.Fatal("logical column mapping", f)
				}
				columns[f.Column] = true
			}
			if record == nil {
				continue
			}
			for _, ref := range record.External {
				if ref.Column != f.Column {
					continue
				}
				if f.Reference == nil || f.Reference.Start != ref.Offset || !bytes.Equal(raw[f.Reference.Start:f.Reference.End], ref.Reference[:]) {
					t.Fatal("external reference source", f)
				}
				if len(ref.Prefix) != 0 && (f.Prefix == nil || !bytes.Equal(raw[f.Prefix.Start:f.Prefix.End], ref.Prefix)) {
					t.Fatal("external prefix source", f)
				}
			}
		}
		if record != nil && len(columns) != len(s.Columns) {
			t.Fatal("missing live column source")
		}
	}
}

func checkSecondarySources(t *testing.T, b []byte, p *SecondaryPage, s SecondarySchema) {
	t.Helper()
	if len(p.Sources) != int(p.Page.Records) {
		t.Fatal("missing secondary sources")
	}
	raw := b[int(p.Page.Number)*PageSize : int(p.Page.Number+1)*PageSize]
	for _, source := range p.Sources {
		var record *SecondaryRecord
		for i := range p.Records {
			if p.Records[i].Offset == source.Offset {
				record = &p.Records[i]
			}
		}
		for i := range p.DeletedRecords {
			if p.DeletedRecords[i].Offset == source.Offset {
				record = &p.DeletedRecords[i]
			}
		}
		for i := range p.Nodes {
			if p.Nodes[i].Offset == source.Offset {
				record = &p.Nodes[i].SecondaryRecord
			}
		}
		if record == nil || source.Node != (p.Page.Level > 0) || source.DeleteMarked != record.DeleteMarked {
			t.Fatal("unmatched secondary source")
		}
		checkRecordSource(t, raw, source, record.Start, record.End)
		for i, f := range source.Fields[:len(s.Fields)] {
			if !s.Fields[i].RowID && (f.Column != s.Fields[i].Column || f.Name != s.Fields[i].Definition.Name || f.Kind != "stored") {
				t.Fatal("secondary column mapping", f)
			}
			if f.Data != nil && !bytes.Equal(raw[f.Data.Start:f.Data.End], record.FieldBytes[i]) {
				t.Fatal("secondary physical field bytes", f)
			}
		}
	}
}

func TestFieldSourceInstant(t *testing.T) {
	b, s := instantFixture(t, "lesson_readd")
	p, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	checkClusteredSources(t, b, p, s)
	// SQL/DDL evidence in testdata/instant: old rows retain the dropped 'a',
	// while current 'a' is synthesized as "again" until row version 3.
	first := p.Sources[0]
	if first.RowVersion != nil || first.Fields[0].Column != 1 || first.Fields[3].Kind != "dropped" || first.Fields[3].Column != -1 || first.Fields[3].Data == nil {
		t.Fatal("unversioned DROP layout", first)
	}
	var copied *FieldSource
	for i := range first.Fields {
		f := &first.Fields[i]
		if f.Column == 0 {
			if f.Kind != "default" || string(f.Default.Data) != "again" {
				t.Fatal("re-added column is not the old dropped field", f)
			}
			copied = f
		}
		if f.Column == 2 && (f.Kind != "default" || !f.Null) {
			t.Fatal("NULL instant default", f)
		}
	}
	if copied == nil {
		t.Fatal("missing re-added default")
	}
	copied.Default.Data[0] ^= 1
	for _, f := range s.Instant.Fields {
		if f.Column == 0 && string(f.Default.Data) != "again" {
			t.Fatal("default aliases schema")
		}
	}
	for i, r := range p.Records {
		v := p.Sources[i].RowVersion
		if (v == nil) != (r.RowVersion == nil) || v != nil && (v.Start != r.Offset-6 || v.End != r.Offset-5 || b[int(s.RootPage)*PageSize+v.Start] != *r.RowVersion) {
			t.Fatal("row-version source")
		}
	}
	b = unzip(t, "testdata/mysql80-patches/8.0.28/instant_compact_mixed.ibd.gz")
	m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
	if err != nil || m.MaterializedSchema == nil {
		t.Fatal(err)
	}
	s = *m.MaterializedSchema
	p, err = TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	checkClusteredSources(t, b, p, s)
	counts, defaults := 0, 0
	for _, source := range p.Sources {
		if source.FieldCount != nil {
			counts++
		}
		for _, f := range source.Fields {
			if f.Kind == "default" {
				defaults++
			}
		}
	}
	if counts == 0 || defaults == 0 {
		t.Fatal("legacy mixed field counts/defaults not exercised")
	}
	b, s = generatedFixture(t, "mixed_instant")
	p, err = TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil || len(p.VirtualColumns) == 0 {
		t.Fatal("separate virtual definitions", err)
	}
	p.VirtualColumns[0].Name = "changed"
	if s.VirtualColumns[0].Name == "changed" {
		t.Fatal("virtual definitions alias schema")
	}
}

func TestFieldSourceErrorsAndScope(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	cause := errors.New("source read failed")
	for _, r := range []io.ReaderAt{nil, localReadResult{err: cause}, localReadResult{n: PageSize - 1}} {
		p, err := TraceClusteredPage(r, int64(len(b)), 4, s)
		if err == nil || p != nil {
			t.Fatal("partial trace on I/O failure")
		}
		if r != nil && !errors.Is(err, cause) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("lost I/O cause", err)
		}
	}
	s.RootPage = 0
	r := &localPageReader{data: b}
	if p, err := TraceClusteredPage(r, int64(len(b)), 4, s); p != nil || !errors.Is(err, ErrUnsupported) || len(r.pages) != 0 {
		t.Fatal("schema validation before I/O", err)
	}
	s.RootPage = 4
	b[4*PageSize+200] ^= 1 // Genuine checksum damage, not resealed.
	if p, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), 4, s); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("corrupt trace accepted", err)
	}
	b, index := secondaryFixture(t, "overlap", "prefix_idx")
	r = &localPageReader{data: b}
	p, err := TraceSecondaryPage(r, int64(len(b)), index.RootPage, index)
	if err != nil || !reflect.DeepEqual(r.pages, []uint32{0, index.RootPage}) {
		t.Fatal("secondary trace I/O scope", err)
	}
	checkSecondarySources(t, b, p, index)
	if p, err := TraceSecondaryPage(nil, int64(len(b)), index.RootPage, index); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil secondary reader", err)
	}
}

func TestFieldSourceLOB(t *testing.T) {
	for _, c := range []struct{ dir, name string }{
		{"testdata/compact", "lesson_compact_initial"},
		{"testdata/mysql5744", "lob_compact_initial"},
		{"testdata/mysql5744", "lob_dynamic_initial"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, s := compactFixture(t, c.dir, c.name)
			size := int64(len(b))
			full, err := ReadMaterialized(bytes.NewReader(b), size, s)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, page := range full.Result.Pages {
				r := &localPageReader{data: b}
				p, err := TraceClusteredPage(r, size, page.Number, s)
				if err != nil || !reflect.DeepEqual(r.pages, []uint32{0, page.Number}) {
					t.Fatal("trace followed LOB", err)
				}
				checkClusteredSources(t, b, p, s)
				for _, record := range p.Records {
					for _, ref := range record.External {
						found++
						var chunks []LOBChunk
						var value []byte
						prefixes := 0
						report, err := StreamLOB(t.Context(), bytes.NewReader(b), size, ref, ScanOptions{}, func(block LOBBlock) error {
							if block.Offset != uint64(len(value)) {
								t.Fatal("LOB logical offset")
							}
							value = append(value, block.Data...)
							if block.Prefix {
								prefixes++
								if !bytes.Equal(block.Data, ref.Prefix) {
									t.Fatal("LOB local prefix")
								}
							} else {
								chunk := block.Chunk
								offset := int(chunk.PageNumber)*PageSize + chunk.Offset
								if !bytes.Equal(block.Data, b[offset:offset+chunk.Length]) {
									t.Fatal("LOB physical bytes")
								}
								chunks = append(chunks, chunk)
							}
							return nil
						})
						if err != nil || !report.Complete || len(ref.Chunks) != 0 || (prefixes == 1) != (len(ref.Prefix) != 0) {
							t.Fatal("explicit LOB completion/provenance", err)
						}
						matched := false
						for _, complete := range full.Result.Records {
							if complete.PageNumber == page.Number && complete.Offset == record.Offset {
								for _, external := range complete.External {
									if external.Column == ref.Column {
										matched = reflect.DeepEqual(chunks, external.Chunks)
									}
								}
							}
						}
						if !matched {
							t.Fatal("stream chunks differ from materialized provenance")
						}
					}
				}
			}
			if found == 0 {
				t.Fatal("no external source exercised")
			}
		})
	}
}

func TestFieldSourceNullableBitmap(t *testing.T) {
	b, s := fixture(t, "null_bitmap_rows")
	p, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	checkClusteredSources(t, b, p, s)
	for _, source := range p.Sources {
		bit := 0
		for _, f := range source.Fields {
			if f.Column >= 0 && s.Columns[f.Column].Nullable {
				if f.NullBit == nil || f.NullBit.Offset != source.Offset-6-bit/8 || f.NullBit.Mask != 1<<uint(bit%8) {
					t.Fatal("multi-byte NULL bitmap order", f)
				}
				bit++
			}
		}
		if bit <= 8 {
			t.Fatal("multi-byte NULL bitmap not exercised")
		}
	}
}

func TestFieldSourceLengths(t *testing.T) {
	b, s := variableFixture(t, "length_rows")
	p, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), 4, s)
	if err != nil {
		t.Fatal(err)
	}
	checkClusteredSources(t, b, p, s)
	for _, source := range p.Sources {
		if source.Offset != 3584 {
			continue
		}
		// Independent manual byte table in chapter 11, SQL id=5. In
		// ascending address order, the two-byte length 256 is 00 81.
		for i, want := range []struct {
			column       int
			length, data ByteSpan
			encoded      []byte
		}{
			{0, ByteSpan{3577, 3578}, ByteSpan{3601, 3853}, []byte{0xfc}},
			{1, ByteSpan{3575, 3577}, ByteSpan{3853, 4109}, []byte{0x00, 0x81}},
			{3, ByteSpan{3574, 3575}, ByteSpan{4109, 4364}, []byte{0xff}},
			{4, ByteSpan{3572, 3574}, ByteSpan{4364, 4620}, []byte{0x00, 0x81}},
			{5, ByteSpan{3570, 3572}, ByteSpan{4620, 4876}, []byte{0x00, 0x81}},
		} {
			f := source.Fields[i+3]
			if f.Column != want.column || f.Length == nil || *f.Length != want.length || f.Data == nil || *f.Data != want.data || !bytes.Equal(b[4*PageSize+f.Length.Start:4*PageSize+f.Length.End], want.encoded) {
				t.Fatal("independent one/two-byte metadata", f, want)
			}
		}
		return
	}
	t.Fatal("documented id=5 origin not found")
}

func TestFieldSourceTruncatedDeletedReference(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "lesson_compact_initial")
	p, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := s.validate()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range p.Sources {
		for _, field := range source.Fields {
			if field.Reference == nil {
				continue
			}
			// Disposable synthetic decoder input, not a valid/repaired page:
			// delete-mark and shorten one external payload to 19 bytes.
			x := append([]byte{}, b[int(s.RootPage)*PageSize:int(s.RootPage+1)*PageSize]...)
			x[source.Offset-5] |= 0x20
			x[field.Length.Start], x[field.Length.End-1] = 19, 0xc0
			if _, err := decodeRecordSources(x, source.Offset, int(p.Page.HeapTop), s, pk, true); !errors.Is(err, ErrCorrupt) {
				t.Fatal("truncated historical reference source", err)
			}
			return
		}
	}
	t.Fatal("no external reference exercised")
}

func FuzzFieldSource(f *testing.F) {
	b, s := instantFixture(f, "lesson_readd")
	secondary, index := secondaryFixture(f, "overlap", "prefix_idx")
	f.Add(false, uint16(120), byte(255))
	f.Add(false, uint16(153), byte(1))
	f.Add(true, uint16(38), byte(255))
	f.Fuzz(func(t *testing.T, useSecondary bool, offset uint16, value byte) {
		data, page := b, s.RootPage
		if useSecondary {
			data, page = secondary, index.RootPage
		}
		x := append([]byte{}, data...)
		x[int(page)*PageSize+int(offset)%PageSize] ^= value
		resealTestPages(x) // Synthetic envelope to exercise metadata boundaries.
		if useSecondary {
			p, err := TraceSecondaryPage(bytes.NewReader(x), int64(len(x)), page, index)
			if err != nil && p != nil {
				t.Fatal("partial secondary trace")
			}
		} else {
			p, err := TraceClusteredPage(bytes.NewReader(x), int64(len(x)), page, s)
			if err != nil && p != nil {
				t.Fatal("partial clustered trace")
			}
		}
	})
}
