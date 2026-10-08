package innodb

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrStopped is an optional callback sentinel for intentional early termination.
// ErrLimit means a configured scan budget was exhausted. Neither means completion.
var (
	ErrStopped = errors.New("innodb: scan stopped")
	ErrLimit   = errors.New("innodb: scan resource limit")
)

// ScanOptions bounds specific resources, not process memory or callback retention.
// Zero fields select the defaults below. CachePages=-1 disables caching.
// MaxPageReads counts requests including cache hits and SDI reads.
// MaxEntries counts queued tree tasks, decoded physical entries and LOB traversal
// entries cumulatively. MaxRows counts live rows. MaxRowBytes counts local record
// bytes plus external suffixes and synthesized default bytes, before LOB decoding.
// MaxLOBBytes applies only to StreamLOB; typed values retain the 16 MiB limit.
type ScanOptions struct {
	CachePages   int    // default 64
	MaxPageReads uint64 // default 1,000,000
	MaxEntries   uint64 // default 1,000,000
	MaxRows      uint64 // default 1,000,000
	MaxRowBytes  uint64 // default 64 MiB
	MaxLOBBytes  uint64 // default 64 MiB
}

// ScanEvent contains exactly one non-nil field. Events follow DFS/key order;
// pages precede their entries. A page event does not certify its descendants.
// Event data may be retained or modified by the callback, independently of the
// scanner. The caller must not mutate schema or the underlying snapshot mid-scan.
type ScanEvent struct {
	Page          *Page
	Node          *NodePointer
	Record        *Record
	DeletedRecord *DeletedRecord
}

// ScanReport is returned even on error. Counters include the callback invocation
// that returned an error. Only Complete=true with nil error certifies the full scan.
// Columns/VirtualColumns describe the selected view; VIRTUAL is never evaluated.
type ScanReport struct {
	Complete                                              bool
	Pages, Nodes, Records, DeletedRecords                 uint64
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64
	Columns                                               []Column
	VirtualColumns                                        []VirtualColumn
}

// Scan synchronously visits a complete-row view using trusted schema. Any emitted
// prefix survives subsequent failure. Return ErrStopped from yield to stop early.
// Cancellation is cooperative and cannot interrupt a blocked ReaderAt or callback.
func Scan(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options ScanOptions, yield func(ScanEvent) error) (ScanReport, error) {
	return scan(ctx, r, size, &schema, options, yield, false)
}

// ScanAuto discovers the complete-row schema from SDI under the same I/O budget.
func ScanAuto(ctx context.Context, r io.ReaderAt, size int64, options ScanOptions, yield func(ScanEvent) error) (ScanReport, error) {
	return scan(ctx, r, size, nil, options, yield, false)
}

// ScanMaterialized explicitly visits stored columns, reporting unmaterialized
// VIRTUAL definitions separately. Event column indexes refer to report.Columns.
func ScanMaterialized(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options ScanOptions, yield func(ScanEvent) error) (ScanReport, error) {
	return scan(ctx, r, size, &schema, options, yield, true)
}

// ScanMaterializedAuto discovers the stored-column view from SDI.
func ScanMaterializedAuto(ctx context.Context, r io.ReaderAt, size int64, options ScanOptions, yield func(ScanEvent) error) (ScanReport, error) {
	return scan(ctx, r, size, nil, options, yield, true)
}

func scan(ctx context.Context, r io.ReaderAt, size int64, schema *Schema, options ScanOptions, yield func(ScanEvent) error, materialized bool) (report ScanReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil scan callback", ErrUnsupported)
	}
	reader, err := newScanReader(ctx, r, options)
	if err != nil {
		return report, err
	}
	defer func() {
		report.PageReads = reader.requests
		report.CacheHits = reader.hits
		report.PhysicalReads = reader.reads
		report.TraversalEntries = reader.entries
	}()
	if schema == nil {
		m, e := InspectTable(reader, size)
		if e != nil {
			return report, e
		}
		schema = m.Schema
		if materialized {
			schema = m.MaterializedSchema
		}
		if schema == nil {
			return report, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
		}
	}
	s := *schema
	if _, err = s.validate(); err != nil {
		return report, err
	}
	if !materialized && len(s.VirtualColumns) > 0 {
		return report, fmt.Errorf("%w: VIRTUAL columns require ScanMaterialized", ErrUnsupported)
	}
	report.Columns = s.Columns
	report.VirtualColumns = s.VirtualColumns
	s.VirtualColumns = nil
	err = walkTree(reader, size, s, func(event ScanEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case event.Page != nil:
			report.Pages++
		case event.Node != nil:
			report.Nodes++
		case event.Record != nil:
			report.Records++
		case event.DeletedRecord != nil:
			report.DeletedRecords++
		}
		if err := yield(event); err != nil {
			return err
		}
		return ctx.Err()
	})
	report.Complete = err == nil
	return report, err
}

type cachedScanPage struct {
	offset int64
	data   []byte
}
type scanReader struct {
	ctx                                  context.Context
	source                               io.ReaderAt
	options                              ScanOptions
	requests, hits, reads, entries, rows uint64
	cache                                map[int64]*list.Element
	lru                                  list.List
}

func newScanReader(ctx context.Context, r io.ReaderAt, o ScanOptions) (*scanReader, error) {
	if ctx == nil || r == nil || o.CachePages < -1 {
		return nil, fmt.Errorf("%w: invalid scan context/reader/cache", ErrUnsupported)
	}
	if o.CachePages == 0 {
		o.CachePages = 64
	}
	if o.MaxPageReads == 0 {
		o.MaxPageReads = 1_000_000
	}
	if o.MaxEntries == 0 {
		o.MaxEntries = 1_000_000
	}
	if o.MaxRows == 0 {
		o.MaxRows = 1_000_000
	}
	if o.MaxRowBytes == 0 {
		o.MaxRowBytes = 64 << 20
	}
	if o.MaxLOBBytes == 0 {
		o.MaxLOBBytes = 64 << 20
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &scanReader{ctx: ctx, source: r, options: o, cache: make(map[int64]*list.Element)}, nil
}
func (r *scanReader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.requests >= r.options.MaxPageReads {
		return 0, fmt.Errorf("%w: page requests", ErrLimit)
	}
	r.requests++
	if e := r.cache[off]; e != nil && len(p) == PageSize {
		r.hits++
		r.lru.MoveToFront(e)
		copy(p, e.Value.(cachedScanPage).data)
		return len(p), nil
	}
	r.reads++
	n, err := r.source.ReadAt(p, off)
	if e := r.ctx.Err(); e != nil {
		return n, e
	}
	if err == nil && n == PageSize && len(p) == PageSize && r.options.CachePages > 0 {
		if r.lru.Len() >= r.options.CachePages {
			e := r.lru.Back()
			old := e.Value.(cachedScanPage)
			delete(r.cache, old.offset)
			copy(old.data, p)
			e.Value = cachedScanPage{off, old.data}
			r.lru.MoveToFront(e)
			r.cache[off] = e
		} else {
			r.cache[off] = r.lru.PushFront(cachedScanPage{off, append([]byte(nil), p...)})
		}
	}
	return n, err
}
func scanCheck(r io.ReaderAt) error {
	if s, ok := r.(*scanReader); ok {
		return s.ctx.Err()
	}
	return nil
}
func scanEntries(r io.ReaderAt, n uint64) error {
	if s, ok := r.(*scanReader); ok {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if n > s.options.MaxEntries-s.entries {
			return fmt.Errorf("%w: traversal entries", ErrLimit)
		}
		s.entries += n
	}
	return nil
}
func scanRow(r io.ReaderAt, e Record, schema Schema) error {
	s, ok := r.(*scanReader)
	if !ok {
		return nil
	}
	if s.rows >= s.options.MaxRows {
		return fmt.Errorf("%w: rows", ErrLimit)
	}
	n := uint64(e.End - e.Start)
	for _, f := range e.External {
		n += uint64(f.Length)
	}
	if schema.Instant != nil {
		for _, c := range e.DefaultColumns {
			for _, f := range schema.Instant.Fields {
				if f.Column == c && f.Default != nil {
					n += uint64(len(f.Default.Data))
				}
			}
		}
	}
	if n > s.options.MaxRowBytes {
		return fmt.Errorf("%w: encoded row bytes", ErrLimit)
	}
	s.rows++
	return s.ctx.Err()
}
