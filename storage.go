package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"sort"
)

// StorageLength holds exact byte lengths. Bucket 0 counts zero; bucket k counts
// [2^(k-1), 2^k-1]. Min/Max are zero when Count is zero. JSON uses decimal strings.
type StorageLength struct {
	Count, Sum, Min, Max uint64
	Buckets              [65]uint64
}

func (s StorageLength) MarshalJSON() ([]byte, error) {
	v := struct {
		Count   uint64 `json:",string"`
		Sum     uint64 `json:",string"`
		Min     uint64 `json:",string"`
		Max     uint64 `json:",string"`
		Buckets [65]string
	}{Count: s.Count, Sum: s.Sum, Min: s.Min, Max: s.Max}
	for i, n := range s.Buckets {
		v.Buckets[i] = fmt.Sprint(n)
	}
	return json.Marshal(v)
}

func (s *StorageLength) observe(n uint64) error {
	if err := storageAdd(&s.Sum, n); err != nil {
		return err
	}
	if err := storageAdd(&s.Count, 1); err != nil {
		return err
	}
	if s.Count == 1 || n < s.Min {
		s.Min = n
	}
	s.Max = max(s.Max, n)
	s.Buckets[bits.Len64(n)]++ // no bucket can exceed the checked Count
	return nil
}

func storageAdd(dst *uint64, n uint64) error {
	v, carry := bits.Add64(*dst, n, 0)
	if carry != 0 {
		return fmt.Errorf("%w: storage counter overflow", ErrLimit)
	}
	*dst = v
	return nil
}

type storageDelta struct {
	dst *uint64
	n   uint64
}

func storageAccumulate(deltas ...storageDelta) error {
	for _, d := range deltas {
		if err := storageAdd(d.dst, d.n); err != nil {
			return err
		}
	}
	return nil
}

// ColumnStorage separates value encoding, actual local bytes and metadata.
// Local includes Prefix/Reference, which must not be added again. Logical applies
// only to text/binary SQL bytes (CHAR right-trimmed in the source charset).
// ExclusiveLOBPages excludes every page shared with another current column.
type ColumnStorage struct {
	Column                  int
	Definition              Column
	MaxEncodedBytes         uint64 `json:",string"`
	Rows                    uint64 `json:",string"`
	Stored                  uint64 `json:",string"`
	Defaults                uint64 `json:",string"`
	Nulls                   uint64 `json:",string"`
	DefaultNulls            uint64 `json:",string"`
	EmptyValues             uint64 `json:",string"`
	ExternalValues          uint64 `json:",string"`
	Encoded, Local          StorageLength
	Logical                 *StorageLength // nil for numeric, ENUM/SET, JSON and geometry
	LengthBytes             uint64         `json:",string"`
	PrefixBytes             uint64         `json:",string"`
	ReferenceBytes          uint64         `json:",string"`
	ReferencedExternalBytes uint64         `json:",string"`
	DefaultEncodedBytes     uint64         `json:",string"`
	ExclusiveLOBPages       uint64         `json:",string"`
	ExclusiveLOBPageBytes   uint64         `json:",string"`
}

// StorageReport covers one complete clustered tree and current non-delete-mark
// stored values. It does not certify allocation or MVCC visibility. Page bytes
// are unique referenced-page capacity, not attributable current payload/free space.
// Failures return nil, so callers never receive a successful-looking prefix.
type StorageReport struct {
	Complete                bool
	SpaceID                 uint32
	IndexID                 uint64 `json:",string"`
	Columns                 []ColumnStorage
	VirtualColumns          []VirtualColumn
	Unverified              []string
	Rows                    uint64 `json:",string"`
	TreePages               uint64 `json:",string"`
	TreePageBytes           uint64 `json:",string"`
	LeafPages               uint64 `json:",string"`
	NodeRecords             uint64 `json:",string"`
	NodeLocalBytes          uint64 `json:",string"`
	DeletedRecords          uint64 `json:",string"`
	DeletedLocalBytes       uint64 `json:",string"`
	LocalRecord             StorageLength
	HeaderBytes             uint64 `json:",string"`
	NullBitmapBytes         uint64 `json:",string"`
	InstantHeaderBytes      uint64 `json:",string"`
	SystemBytes             uint64 `json:",string"`
	DroppedPayloadBytes     uint64 `json:",string"`
	DroppedLengthBytes      uint64 `json:",string"`
	ReferencedExternalBytes uint64 `json:",string"`
	UniqueExternalBytes     uint64 `json:",string"`
	LOBReferencedPages      uint64 `json:",string"`
	LOBReferencedPageBytes  uint64 `json:",string"`
	SharedLOBPages          uint64 `json:",string"`
	SharedLOBPageBytes      uint64 `json:",string"`
	PageReads               uint64 `json:",string"`
	CacheHits               uint64 `json:",string"`
	PhysicalReads           uint64 `json:",string"`
	TraversalEntries        uint64 `json:",string"`
}

// AnalyzeStorage measures trusted single-table storage by scanning all current
// values. VIRTUAL definitions require AnalyzeStorageMaterialized explicitly.
func AnalyzeStorage(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options ScanOptions) (*StorageReport, error) {
	return analyzeStorage(ctx, r, size, schema, options, false)
}

// AnalyzeStorageMaterialized separately reports VIRTUAL definitions without
// evaluating them or manufacturing NULL values/physical storage for them.
func AnalyzeStorageMaterialized(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options ScanOptions) (*StorageReport, error) {
	return analyzeStorage(ctx, r, size, schema, options, true)
}

type storageLOBPage struct {
	owner int // -1 means shared across current columns
	data  []ByteSpan
}

type storageAnalysis struct {
	out *StorageReport
	lob map[uint32]*storageLOBPage
}

func analyzeStorage(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options ScanOptions, materialized bool) (*StorageReport, error) {
	reader, err := newScanReader(ctx, r, options)
	if err != nil {
		return nil, err
	}
	pk, err := schema.validate()
	if err != nil {
		return nil, err
	}
	if !materialized && len(schema.VirtualColumns) != 0 {
		return nil, fmt.Errorf("%w: VIRTUAL requires AnalyzeStorageMaterialized", ErrUnsupported)
	}
	out := &StorageReport{SpaceID: schema.SpaceID, IndexID: schema.IndexID,
		VirtualColumns: append([]VirtualColumn(nil), schema.VirtualColumns...),
		Unverified:     []string{"transaction visibility and snapshot provenance", "FSP allocation and unreferenced pages", "other indexes and cross-index correspondence", "historical row/LOB values and VIRTUAL expressions", "reclaimable disk space"}}
	selected := make(map[int]bool)
	for i, c := range schema.Columns {
		column := ColumnStorage{Column: i, Definition: c, MaxEncodedBytes: uint64(c.fixedWidth())}
		if c.isVariable() {
			column.MaxEncodedBytes = c.variableMaxBytes()
		}
		if c.isText() || c.isBinary() {
			column.Logical = &StorageLength{}
		}
		out.Columns = append(out.Columns, column)
		selected[i] = true
	}
	schema.VirtualColumns = nil
	selection, err := prepareQuery(KeyRange{}, schema, pk)
	if err != nil {
		return nil, err
	}
	selection.deferValues, selection.fullScan, selection.captureSources = true, true, true
	a := storageAnalysis{out: out, lob: map[uint32]*storageLOBPage{}}
	err = walkSelectedTree(reader, size, schema, func(e ScanEvent) error {
		switch {
		case e.Page != nil:
			if err := storageAccumulate(storageDelta{&out.TreePages, 1}, storageDelta{&out.TreePageBytes, PageSize}); err != nil {
				return err
			}
			if e.Page.Level == 0 {
				return storageAdd(&out.LeafPages, 1)
			}
		case e.Node != nil:
			return storageAccumulate(storageDelta{&out.NodeRecords, 1}, storageDelta{&out.NodeLocalBytes, uint64(e.Node.End - e.Node.Start)})
		case e.DeletedRecord != nil:
			return storageAccumulate(storageDelta{&out.DeletedRecords, 1}, storageDelta{&out.DeletedLocalBytes, uint64(e.DeletedRecord.End - e.DeletedRecord.Start)})
		case e.Record != nil:
			record, err := materializeProjection(reader, size, *e.Record, schema, selected)
			if err != nil {
				return err
			}
			return a.record(record)
		}
		return ctx.Err()
	}, selection)
	if err != nil {
		return nil, err
	}
	if err := a.finish(ctx); err != nil {
		return nil, err
	}
	out.PageReads, out.CacheHits, out.PhysicalReads, out.TraversalEntries = reader.requests, reader.hits, reader.reads, reader.entries
	out.Complete = true
	return out, nil
}

func storageSpan(s *ByteSpan) uint64 {
	if s == nil {
		return 0
	}
	return uint64(s.End - s.Start)
}

func (a *storageAnalysis) record(r Record) error {
	out := a.out
	if err := out.LocalRecord.observe(uint64(r.End - r.Start)); err != nil {
		return err
	}
	instant := storageSpan(r.source.RowVersion) + storageSpan(r.source.FieldCount)
	var lengths, system, dropped, droppedLengths uint64
	external := make(map[int]ExternalField, len(r.External))
	for _, f := range r.External {
		external[f.Column] = f
		a.lobPage(f.FirstPage, f.Column, nil)
		for _, chunk := range f.Chunks {
			a.lobPage(chunk.PageNumber, f.Column, &ByteSpan{chunk.Offset, chunk.Offset + chunk.Length})
			if chunk.IndexPage != 0 {
				a.lobPage(chunk.IndexPage, f.Column, nil)
			}
		}
	}
	for _, f := range r.source.Fields {
		lengths += storageSpan(f.Length)
		switch f.Kind {
		case "system":
			system += storageSpan(f.Data)
		case "dropped":
			dropped += storageSpan(f.Data)
			droppedLengths += storageSpan(f.Length)
		default:
			if err := a.column(r, f, external[f.Column]); err != nil {
				return err
			}
		}
	}
	return storageAccumulate(
		storageDelta{&out.Rows, 1}, storageDelta{&out.HeaderBytes, 5},
		storageDelta{&out.NullBitmapBytes, uint64(r.Offset-r.Start-5) - lengths - instant},
		storageDelta{&out.InstantHeaderBytes, instant}, storageDelta{&out.SystemBytes, system},
		storageDelta{&out.DroppedPayloadBytes, dropped}, storageDelta{&out.DroppedLengthBytes, droppedLengths})
}

func (a *storageAnalysis) column(r Record, f FieldSource, external ExternalField) error {
	c := &a.out.Columns[f.Column]
	if err := storageAdd(&c.Rows, 1); err != nil {
		return err
	}
	encoded := storageSpan(f.Data)
	if f.Kind == "default" {
		encoded = uint64(len(f.Default.Data))
		if err := storageAccumulate(storageDelta{&c.Defaults, 1}, storageDelta{&c.DefaultEncodedBytes, encoded}); err != nil {
			return err
		}
		if f.Null {
			if err := storageAdd(&c.DefaultNulls, 1); err != nil {
				return err
			}
		}
	} else {
		if err := storageAccumulate(storageDelta{&c.Stored, 1}, storageDelta{&c.LengthBytes, storageSpan(f.Length)}); err != nil {
			return err
		}
		if !f.Null {
			if err := c.Local.observe(encoded); err != nil {
				return err
			}
		}
	}
	if f.Null {
		return storageAdd(&c.Nulls, 1)
	}
	if f.Reference != nil {
		encoded = uint64(external.Length) + uint64(len(external.Prefix))
		if err := storageAccumulate(storageDelta{&c.ExternalValues, 1},
			storageDelta{&c.PrefixBytes, storageSpan(f.Prefix)}, storageDelta{&c.ReferenceBytes, storageSpan(f.Reference)},
			storageDelta{&c.ReferencedExternalBytes, uint64(external.Length)}, storageDelta{&a.out.ReferencedExternalBytes, uint64(external.Length)}); err != nil {
			return err
		}
	}
	if err := c.Encoded.observe(encoded); err != nil {
		return err
	}
	if c.Logical != nil {
		logical := encoded
		if c.Definition.isText() {
			data := r.TextBytes[f.Column]
			if c.Definition.Type == "CHAR" {
				data = bytes.TrimRight(data, " ")
			}
			logical = uint64(len(data))
		}
		if err := c.Logical.observe(logical); err != nil {
			return err
		}
		if logical == 0 {
			return storageAdd(&c.EmptyValues, 1)
		}
	}
	return nil
}

func (a *storageAnalysis) lobPage(number uint32, column int, span *ByteSpan) {
	p := a.lob[number]
	if p == nil {
		p = &storageLOBPage{owner: column}
		a.lob[number] = p
	} else if p.owner != column {
		p.owner = -1
	}
	if span != nil {
		p.data = append(p.data, *span)
	}
}

func (a *storageAnalysis) finish(ctx context.Context) error {
	for _, p := range a.lob {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.out.LOBReferencedPages++ // bounded by the file's uint32 page space
		if p.owner < 0 {
			a.out.SharedLOBPages++
		} else {
			a.out.Columns[p.owner].ExclusiveLOBPages++
		}
		sort.Slice(p.data, func(i, j int) bool { return p.data[i].Start < p.data[j].Start })
		end := 0
		for _, s := range p.data {
			if s.End > end {
				if err := storageAdd(&a.out.UniqueExternalBytes, uint64(s.End-max(end, s.Start))); err != nil {
					return err
				}
				end = s.End
			}
		}
	}
	a.out.LOBReferencedPageBytes = a.out.LOBReferencedPages * PageSize
	a.out.SharedLOBPageBytes = a.out.SharedLOBPages * PageSize
	for i := range a.out.Columns {
		c := &a.out.Columns[i]
		c.ExclusiveLOBPageBytes = c.ExclusiveLOBPages * PageSize
	}
	return ctx.Err()
}
