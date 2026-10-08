package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestStreamLOBOverLimit(t *testing.T) {
	b, s := largeLOBFixture(t, "long_over_limit")
	r := bytes.NewReader(b)
	size := int64(len(b))
	pk, err := s.validate()
	if err != nil {
		t.Fatal(err)
	}
	page, err := readPage(r, size, s.RootPage)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseIndex(page, s)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := decodePage(page, p, s, pk)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].External) != 1 {
		t.Fatal("fixture layout")
	}
	source := entries[0].External[0]
	if source.Length != MaxLOBValueBytes+1 {
		t.Fatal("fixture no longer over limit")
	}
	var expected [][]json.RawMessage
	if err = json.Unmarshal(unzip(t, "testdata/large_lob/long_over_limit.expected.json.gz"), &expected); err != nil {
		t.Fatal(err)
	}
	var hexValue string
	if err = json.Unmarshal(expected[0][1], &hexValue); err != nil {
		t.Fatal(err)
	}
	sqlBytes, err := hex.DecodeString(hexValue)
	if err != nil {
		t.Fatal(err)
	}
	wanted := sha256.Sum256(sqlBytes)
	h := sha256.New()
	report, err := StreamLOB(context.Background(), r, size, source, ScanOptions{CachePages: 2}, func(block LOBBlock) error {
		if len(block.Data) > PageSize {
			t.Fatal("unbounded block")
		}
		_, e := h.Write(block.Data)
		return e
	})
	if err != nil || !report.Complete || report.Bytes != uint64(len(sqlBytes)) || !bytes.Equal(h.Sum(nil), wanted[:]) {
		t.Fatal("SQL SHA256/length", report, err)
	}
	if report.CacheHits == 0 {
		t.Fatal("external index cache not used")
	}
	atomic, err := Read(r, size, s)
	if atomic != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("materialization cap changed", err)
	}
	scan, err := Scan(context.Background(), r, size, s, ScanOptions{}, func(ScanEvent) error { return nil })
	if scan.Complete || scan.Records != 0 || !errors.Is(err, ErrUnsupported) {
		t.Fatal("typed scan cap changed", scan, err)
	}
	t.Logf("SQL-confirmed %d bytes, %d blocks, %d requests, %d physical reads", report.Bytes, report.Blocks, report.PageReads, report.PhysicalReads)
}

func TestStreamLOBControl(t *testing.T) {
	ctx := context.Background()
	for _, dir := range []string{"testdata/compact", "testdata/compact-legacy"} {
		t.Run(dir, func(t *testing.T) {
			b, s := compactFixture(t, dir, "lesson_compact_initial")
			r := bytes.NewReader(b)
			size := int64(len(b))
			result, err := Read(r, size, s)
			if err != nil {
				t.Fatal(err)
			}
			source := result.Records[0].External[0]
			original := source
			var blocks []LOBBlock
			full, err := StreamLOB(ctx, r, size, source, ScanOptions{}, func(block LOBBlock) error { blocks = append(blocks, block); return nil })
			if err != nil || !full.Complete || !reflect.DeepEqual(source, original) {
				t.Fatal(full, err)
			}
			joined := []byte{}
			for _, block := range blocks {
				joined = append(joined, block.Data...)
			}
			expected := result.Records[0].Values[source.Column].(string)
			if string(joined) != expected || !blocks[0].Prefix || len(blocks[0].Data) != 768 {
				t.Fatal("owned blocks/prefix")
			}
			sink := func(LOBBlock) error { return nil }
			for _, o := range []ScanOptions{{MaxLOBBytes: full.Bytes - 1}, {MaxPageReads: 1}, {MaxEntries: 1}} {
				p, e := StreamLOB(ctx, r, size, source, o, sink)
				if p.Complete || !errors.Is(e, ErrLimit) {
					t.Fatal("limit", o, p, e)
				}
			}
			p, e := StreamLOB(ctx, r, size, source, ScanOptions{MaxLOBBytes: full.Bytes, MaxEntries: full.TraversalEntries, MaxPageReads: full.PageReads}, sink)
			if e != nil || !p.Complete {
				t.Fatal("exact LOB budget", p, e)
			}
			p, e = StreamLOB(ctx, r, size, source, ScanOptions{}, func(LOBBlock) error { return ErrStopped })
			if p.Complete || p.Blocks != 1 || p.Bytes != 768 || !errors.Is(e, ErrStopped) {
				t.Fatal("stop", p, e)
			}
			c, cancel := context.WithCancel(ctx)
			p, e = StreamLOB(c, r, size, source, ScanOptions{}, func(LOBBlock) error { cancel(); return nil })
			if p.Complete || p.Blocks != 1 || !errors.Is(e, context.Canceled) {
				t.Fatal("cancel", p, e)
			}
			// Corrupt a later actual DATA/BLOB page; earlier blocks are valid but incomplete.
			damaged := append([]byte(nil), b...)
			chunk := source.Chunks[1]
			damaged[int(chunk.PageNumber)*PageSize+chunk.Offset] ^= 1
			p, e = StreamLOB(ctx, bytes.NewReader(damaged), size, source, ScanOptions{}, sink)
			if p.Complete || p.Blocks < 2 || !errors.Is(e, ErrCorrupt) {
				t.Fatal("late CRC", p, e)
			}
			// Inject later I/O failure without changing the file's advertised size.
			failing := &scanFailReader{ReaderAt: r, fail: 3, err: io.ErrClosedPipe}
			p, e = StreamLOB(ctx, failing, size, source, ScanOptions{CachePages: -1}, sink)
			if p.Complete || p.Blocks == 0 || !errors.Is(e, io.ErrClosedPipe) {
				t.Fatal("late I/O", p, e)
			}
			p, e = StreamLOB(ctx, r, size, source, ScanOptions{}, func(block LOBBlock) error {
				for i := range block.Data {
					block.Data[i] = 0
				}
				return nil
			})
			if e != nil || !p.Complete || source.Prefix[0] == 0 {
				t.Fatal("callback mutation leaked", e)
			}
			// Decoded Version=0 on legacy fields is deliberately ignored: Reference is authoritative.
			source.Version = 999
			source.Length = 0
			source.FirstPage = 0
			source.Chunks = nil
			if p, e = StreamLOB(ctx, r, size, source, ScanOptions{}, sink); e != nil || !p.Complete {
				t.Fatal("derived input fields used", e)
			}
			source.Prefix = []byte{1}
			if _, e = StreamLOB(ctx, r, size, source, ScanOptions{}, sink); !errors.Is(e, ErrUnsupported) {
				t.Fatal("prefix", e)
			}
			source.Prefix = nil
			source.Reference[12] |= 0x20
			if _, e = StreamLOB(ctx, r, size, source, ScanOptions{}, sink); !errors.Is(e, ErrUnsupported) {
				t.Fatal("modified reference", e)
			}
			if _, e = StreamLOB(ctx, r, size, original, ScanOptions{}, nil); !errors.Is(e, ErrUnsupported) {
				t.Fatal("nil callback", e)
			}
			if _, e = StreamLOB(nil, r, size, original, ScanOptions{}, sink); !errors.Is(e, ErrUnsupported) {
				t.Fatal("nil context", e)
			}
		})
	}
}

func FuzzStreaming(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	f.Add(uint16(4), uint16(120), []byte{0}, uint8(1))
	f.Fuzz(func(t *testing.T, page, offset uint16, data []byte, stop uint8) {
		if len(data) > PageSize {
			return
		}
		copyBytes := append([]byte(nil), b...)
		p := int(page) % (len(b) / PageSize) * PageSize
		o := int(offset) % PageSize
		copy(copyBytes[p+o:p+PageSize], data)
		resealTestPages(copyBytes)
		seen := uint64(0)
		report, err := Scan(context.Background(), bytes.NewReader(copyBytes), int64(len(copyBytes)), s, ScanOptions{MaxPageReads: 100, MaxEntries: 1000}, func(e ScanEvent) error {
			if e.Record != nil {
				seen++
				if seen == uint64(stop) {
					return ErrStopped
				}
			}
			return nil
		})
		if report.Records != seen || report.Complete != (err == nil) {
			t.Fatal("completion protocol")
		}
	})
}

func FuzzStreamLOB(f *testing.F) {
	fixtures := make([][]byte, 2)
	sources := make([]ExternalField, 2)
	for i, dir := range []string{"testdata/compact", "testdata/compact-legacy"} {
		b, s := compactFixture(f, dir, "lesson_compact_initial")
		result, err := Read(bytes.NewReader(b), int64(len(b)), s)
		if err != nil {
			f.Fatal(err)
		}
		fixtures[i] = b
		sources[i] = result.Records[0].External[0]
	}
	f.Add(uint8(0), uint16(64), []byte{0, 0, 0, 0}, uint8(0))
	f.Add(uint8(1), uint16(42), []byte{255, 255, 255, 255}, uint8(0))
	f.Add(uint8(0), uint16(96), []byte{0}, uint8(2))
	f.Fuzz(func(t *testing.T, kind uint8, offset uint16, data []byte, stop uint8) {
		if len(data) > PageSize {
			return
		}
		which := int(kind % 2)
		b := append([]byte(nil), fixtures[which]...)
		source := sources[which]
		p := int(source.FirstPage) * PageSize
		o := int(offset) % PageSize
		copy(b[p+o:p+PageSize], data)
		resealTestPages(b)
		blocks, bytesSeen := uint64(0), uint64(0)
		report, err := StreamLOB(context.Background(), bytes.NewReader(b), int64(len(b)), source, ScanOptions{MaxPageReads: 256, MaxEntries: 4096}, func(block LOBBlock) error {
			if block.Offset != bytesSeen {
				t.Fatal("offset")
			}
			blocks++
			bytesSeen += uint64(len(block.Data))
			if blocks == uint64(stop) {
				return ErrStopped
			}
			return nil
		})
		if report.Blocks != blocks || report.Bytes != bytesSeen || report.Complete != (err == nil) {
			t.Fatal("block completion protocol")
		}
	})
}
