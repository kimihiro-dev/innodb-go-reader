package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return n, err
}

// WriteJSON writes a single versioned object, with exact decimal string integers.
// A write failure may leave a prefix; it does not change Report.Complete.
func WriteJSON(ctx context.Context, w io.Writer, r *Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := json.NewEncoder(contextWriter{ctx, w}).Encode(r); err != nil {
		return err
	}
	return ctx.Err()
}

func percent(r *Ratio) string {
	if r == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f%%", 100*float64(r.Numerator)/float64(r.Denominator))
}
func optionalID(n *uint64) string {
	if n == nil {
		return "—"
	}
	return strconv.FormatUint(*n, 10)
}
func optionalType(n *uint16) string {
	if n == nil {
		return "—"
	}
	return strconv.FormatUint(uint64(*n), 10)
}

// WriteTable presents the same groups as JSON. Ratios are rounded for display
// only, in percent; all counts and IDs keep their exact integer representation.
func WriteTable(ctx context.Context, w io.Writer, r *Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w = contextWriter{ctx, w}
	if _, err := fmt.Fprintf(w, "view=%s scope=%s complete=%t\nfile_pages=%d file_bytes=%d page_size=%d index_id=%s level=%s\n",
		r.View, r.Scope, r.Complete, r.Source.FilePages, r.Source.FileBytes, r.Source.PageSize,
		optionalID(r.Filter.IndexID), optionalType(r.Filter.Level)); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	emit := func(values ...any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cells := make([]string, len(values))
		for i, v := range values {
			cells[i] = fmt.Sprint(v)
		}
		_, err := fmt.Fprintln(tw, strings.Join(cells, "\t"))
		return err
	}
	switch groups := r.Groups.(type) {
	case []Summary:
		if err := emit("METRIC", "VALUE", "UNIT"); err != nil {
			return err
		}
		for _, g := range groups {
			rows := []struct {
				name  string
				value any
				unit  string
			}{
				{"file_pages", g.FilePages, "pages"}, {"file_bytes", g.FileBytes, "bytes"},
				{"used_pages", g.UsedPages, "pages"}, {"free_pages", g.FreePages, "pages"},
				{"uninitialized_pages", g.UninitializedPages, "pages"}, {"tail_pages", g.TailPages, "pages"},
				{"zero_pages", g.ZeroPages, "pages (overlapping observation)"},
				{"index_count", g.IndexCount, "indexes"}, {"segment_count", g.SegmentCount, "segments"},
				{"unknown_used_pages", g.UnknownUsedPages, "pages"}, {"used_share", percent(g.UsedShare), "used/file pages"},
			}
			for _, row := range rows {
				if err := emit(row.name, row.value, row.unit); err != nil {
					return err
				}
			}
		}
	case []PageType:
		if err := emit("TYPE_CODE", "TYPE_NAME", "PAGES", "BYTES", "SHARE_OF_USED_PAGES"); err != nil {
			return err
		}
		for _, g := range groups {
			if err := emit(g.TypeCode, g.TypeName, g.Pages, g.Bytes, percent(g.ShareOfUsedPages)); err != nil {
				return err
			}
		}
	case []Index:
		if err := emit("INDEX_ID", "KIND", "ROOT_PAGE", "HEIGHT", "PAGES", "LEAF_PAGES", "INTERNAL_PAGES", "LEAF_LINKED_RECORDS", "LEAF_DELETE_MARKED", "LEAF_NON_DELETED", "LOB_ALLOCATED_PAGES"); err != nil {
			return err
		}
		for _, g := range groups {
			if err := emit(g.IndexID, g.Kind, g.RootPage, g.Height, g.Pages, g.LeafPages, g.InternalPages, g.LeafLinkedRecords, g.LeafDeleteMarked, g.LeafNonDeleted, g.LOBAllocatedPages); err != nil {
				return err
			}
		}
	case []Level:
		if err := emit("INDEX_ID", "LEVEL", "PAGES", "LINKED_RECORDS", "DELETE_MARKED", "FREE_LIST_RECORDS", "HEAP_BYTES", "LAYOUT_USED_BYTES", "GARBAGE_BYTES", "CONTIGUOUS_FREE_BYTES", "DIRECTORY_BYTES", "LAYOUT_OCCUPANCY", "MIN", "MAX"); err != nil {
			return err
		}
		for _, g := range groups {
			if err := emit(g.IndexID, g.Level, g.Pages, g.LinkedRecords, g.DeleteMarkedRecords, g.FreeListRecords, g.HeapBytes, g.LayoutUsedBytes, g.GarbageBytes, g.ContiguousFreeBytes, g.DirectoryBytes, percent(g.LayoutOccupancy), percent(g.LayoutOccupancyMin), percent(g.LayoutOccupancyMax)); err != nil {
				return err
			}
		}
	case []Segment:
		if err := emit("SEGMENT_ID", "INDEX_ID", "ROLE", "USED_PAGES", "RESERVED_PAGES", "RESERVED_UNUSED", "FRAGMENT_PAGES", "FREE_EXTENTS", "PARTIAL_EXTENTS", "FULL_EXTENTS", "ALLOCATION_UTILIZATION"); err != nil {
			return err
		}
		for _, g := range groups {
			if err := emit(g.SegmentID, optionalID(g.IndexID), g.Role, g.UsedPages, g.ReservedPages, g.ReservedUnusedPages, g.FragmentPageCount, g.FreeExtentCount, g.PartialExtentCount, g.FullExtentCount, percent(g.AllocationUtilization)); err != nil {
				return err
			}
		}
	case []Region:
		if err := emit("FIRST_PAGE", "END_PAGE_EXCLUSIVE", "PAGES", "ALLOCATION", "TYPE_CODE", "INDEX_ID", "SEGMENT_ID"); err != nil {
			return err
		}
		for _, g := range groups {
			if err := emit(g.FirstPage, g.EndPage, g.Pages, g.Allocation, optionalType(g.TypeCode), optionalID(g.IndexID), optionalID(g.SegmentID)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("stats: unsupported table groups %T", r.Groups)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return ctx.Err()
}
