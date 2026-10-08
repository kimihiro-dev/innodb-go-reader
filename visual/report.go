// Package visual builds bounded, self-contained learning reports from the public
// InnoDB parsers. It does not implement another physical page decoder.
package visual

import (
	"context"
	"encoding/hex"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"path/filepath"
	"strings"
)

// Options selects explicit layers. Index scans one complete index; Pages selects
// raw bytes and record provenance to retain, never a partial validation shortcut.
type Options struct {
	Title, Index, ManualDir string
	Pages                   []uint32
	Materialized            bool
	Space                   innodb.SpaceOptions
	Scan                    innodb.ScanOptions
	MaxReadCalls            uint64 // all underlying ReadAt calls; default 1,000,000
	MaxDetails              uint64 // selected record details; default 10,000
	MaxReportBytes          uint64 // embedded JSON, default 64 MiB; not a process RSS bound
}
type Edge struct {
	Parent, Child      uint32
	Start, Offset, End int
	Minimum            bool
}
type Detail struct {
	Kind               string
	Page               uint32
	Start, Origin, End int
	Header             [5]byte
	RowID              *uint64                `json:",omitempty"`
	Transaction        *[6]byte               `json:",omitempty"`
	RollPointer        *[7]byte               `json:",omitempty"`
	RowVersion         *uint8                 `json:",omitempty"`
	DefaultColumns     []int                  `json:",omitempty"`
	External           []innodb.ExternalField `json:",omitempty"`
}
type RawPage struct {
	Number uint32
	Hex    string
}
type Report struct {
	Version                 int
	Title, Index, ManualDir string
	Space                   *innodb.SpaceReport
	IndexRoot               *uint32 `json:",omitempty"`
	TreePages               []innodb.Page
	Edges                   []Edge
	Columns                 []innodb.Column
	VirtualColumns          []innodb.VirtualColumn
	SecondaryFields         []innodb.SecondaryField
	Details                 []Detail
	RawPages                []RawPage
	ReadCalls               uint64
	ScanReport              any `json:",omitempty"`
	maxBytes                uint64
}

type sourceReader struct {
	ctx          context.Context
	source       io.ReaderAt
	calls, limit uint64
}

func (r *sourceReader) ReadAt(b []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.calls >= r.limit {
		return 0, fmt.Errorf("%w: visualization source reads", innodb.ErrLimit)
	}
	r.calls++
	n, err := r.source.ReadAt(b, offset)
	if err == nil && n != len(b) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// Build returns nil on any failure, including a requested detail layer. The
// caller owns the reader and must keep the snapshot stable for the entire call.
func Build(ctx context.Context, r io.ReaderAt, size int64, o Options) (*Report, error) {
	if ctx == nil || r == nil {
		return nil, fmt.Errorf("%w: nil visualization context/reader", innodb.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.Space.MaxPages == 0 {
		o.Space.MaxPages = 100000
	}
	if o.MaxReadCalls == 0 {
		o.MaxReadCalls = 1000000
	}
	if o.MaxDetails == 0 {
		o.MaxDetails = 10000
	}
	if o.MaxReportBytes == 0 {
		o.MaxReportBytes = 64 << 20
	}
	if o.ManualDir != "" && !filepath.IsAbs(o.ManualDir) {
		return nil, fmt.Errorf("%w: manual directory must be absolute", innodb.ErrUnsupported)
	}
	if o.Materialized && o.Index == "" {
		return nil, fmt.Errorf("%w: materialized requires an index", innodb.ErrUnsupported)
	}
	if len(o.Pages) > 256 {
		return nil, fmt.Errorf("%w: at most 256 raw pages", innodb.ErrLimit)
	}
	selected := map[uint32]bool{}
	for _, p := range o.Pages {
		if selected[p] || int64(p) >= size/innodb.PageSize {
			return nil, fmt.Errorf("%w: duplicate/outside raw page %d", innodb.ErrCorrupt, p)
		}
		selected[p] = true
	}
	source := &sourceReader{ctx: ctx, source: r, limit: o.MaxReadCalls}
	space, err := innodb.AnalyzeSpace(ctx, source, size, o.Space)
	if err != nil {
		return nil, err
	}
	out := &Report{Version: 1, Title: o.Title, Index: o.Index, ManualDir: o.ManualDir, Space: space, maxBytes: o.MaxReportBytes}
	if out.Title == "" {
		out.Title = "InnoDB 页结构报告"
	}
	// Unknown-page bytes remain available only through explicitly selected pages.
	// Space analysis has already retained/validated its normal report internally.
	for i := range out.Space.Pages {
		out.Space.Pages[i].Raw = nil
	}
	var detailBytes uint64
	add := func(d Detail) error {
		if !selected[d.Page] {
			return nil
		}
		if uint64(len(out.Details)) >= o.MaxDetails {
			return fmt.Errorf("%w: selected record details", innodb.ErrLimit)
		}
		b, err := exactJSON(d)
		if err != nil {
			return err
		}
		if uint64(len(b)) > o.MaxReportBytes-detailBytes {
			return fmt.Errorf("%w: detail JSON bytes", innodb.ErrLimit)
		}
		detailBytes += uint64(len(b))
		out.Details = append(out.Details, d)
		return nil
	}
	if o.Index != "" {
		meta, err := innodb.InspectTable(source, size)
		if err != nil {
			return nil, err
		}
		var index *innodb.IndexMetadata
		for i := range meta.Indexes {
			if meta.Indexes[i].Name == o.Index {
				index = &meta.Indexes[i]
				break
			}
		}
		if index == nil {
			return nil, fmt.Errorf("%w: unknown index %q", innodb.ErrUnsupported, o.Index)
		}
		out.IndexRoot = &index.RootPage
		if index.Clustered {
			schema := meta.Schema
			if o.Materialized {
				schema = meta.MaterializedSchema
			}
			if schema == nil {
				return nil, fmt.Errorf("%w: %s", innodb.ErrUnsupported, strings.Join(meta.Issues, "; "))
			}
			scan := innodb.Scan
			if o.Materialized {
				scan = innodb.ScanMaterialized
			}
			report, err := scan(ctx, source, size, *schema, o.Scan, func(e innodb.ScanEvent) error {
				switch {
				case e.Page != nil:
					out.TreePages = append(out.TreePages, *e.Page)
				case e.Node != nil:
					n := e.Node
					out.Edges = append(out.Edges, Edge{n.PageNumber, n.ChildPage, n.Start, n.Offset, n.End, n.Minimum})
				case e.Record != nil:
					r := e.Record
					return add(Detail{Kind: "record", Page: r.PageNumber, Start: r.Start, Origin: r.Offset, End: r.End, Header: r.Header, RowID: r.RowID, Transaction: &r.Transaction, RollPointer: &r.RollPointer, RowVersion: r.RowVersion, DefaultColumns: r.DefaultColumns, External: r.External})
				case e.DeletedRecord != nil:
					r := e.DeletedRecord
					return add(Detail{Kind: "deleted", Page: r.PageNumber, Start: r.Start, Origin: r.Offset, End: r.End, Header: r.Header, RowID: r.RowID, Transaction: &r.Transaction, RollPointer: &r.RollPointer})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			out.ScanReport = report
			out.Columns = report.Columns
			out.VirtualColumns = report.VirtualColumns
		} else {
			if o.Materialized {
				return nil, fmt.Errorf("%w: materialized applies only to clustered index", innodb.ErrUnsupported)
			}
			report, err := innodb.ScanSecondaryAuto(ctx, source, size, o.Index, o.Scan, func(e innodb.SecondaryEvent) error {
				switch {
				case e.Page != nil:
					out.TreePages = append(out.TreePages, *e.Page)
				case e.Node != nil:
					n := e.Node
					out.Edges = append(out.Edges, Edge{n.PageNumber, n.ChildPage, n.Start, n.Offset, n.End, n.Minimum})
				case e.Record != nil || e.DeletedRecord != nil:
					r := e.Record
					kind := "secondary"
					if r == nil {
						r = e.DeletedRecord
						kind = "secondary-deleted"
					}
					return add(Detail{Kind: kind, Page: r.PageNumber, Start: r.Start, Origin: r.Offset, End: r.End, Header: r.Header, RowID: r.RowID})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			out.ScanReport = report
			out.SecondaryFields = report.Schema.Fields
		}
	}
	for _, p := range o.Pages {
		b := make([]byte, innodb.PageSize)
		if _, err := source.ReadAt(b, int64(p)*innodb.PageSize); err != nil {
			return nil, err
		}
		out.RawPages = append(out.RawPages, RawPage{p, hex.EncodeToString(b)})
	}
	out.ReadCalls = source.calls
	if _, err := out.payload(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
