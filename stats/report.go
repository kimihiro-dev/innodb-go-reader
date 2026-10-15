// Package stats aggregates one validated space analysis into physical statistics.
// Its reports do not certify SQL row values, MVCC visibility or tree navigation.
package stats

import (
	"context"
	"fmt"
	innodb "github.com/kimihiro-dev/innodb-go-reader"
	"io"
	"sort"
)

// Options filters groups after whole-file analysis. Empty View means summary.
// Zero space budgets use AnalyzeSpace defaults. IndexID and Level are optional.
type Options struct {
	View    string
	IndexID *uint64
	Level   *uint16
	Space   innodb.SpaceOptions
}

// Validate checks view/filter combinations without reading the source.
func Validate(o Options) error {
	switch o.View {
	case "", "summary", "page-types", "indexes", "levels", "segments", "regions":
	default:
		return fmt.Errorf("%w: stats view %q", innodb.ErrUnsupported, o.View)
	}
	if o.IndexID != nil && (*o.IndexID == 0 || o.View != "indexes" && o.View != "levels" && o.View != "segments") {
		return fmt.Errorf("%w: --index-id must be nonzero and requires indexes, levels or segments", innodb.ErrUnsupported)
	}
	if o.Level != nil && (o.View != "levels" || o.IndexID == nil) {
		return fmt.Errorf("%w: --level requires levels and --index-id", innodb.ErrUnsupported)
	}
	return nil
}

// Ratio keeps exact integers; a zero denominator is represented by a nil Ratio.
type Ratio struct {
	Numerator   uint64 `json:"numerator,string"`
	Denominator uint64 `json:"denominator,string"`
}

func ratio(n, d uint64) *Ratio {
	if d == 0 {
		return nil
	}
	return &Ratio{n, d}
}

type Source struct {
	SpaceID   uint32 `json:"space_id,string"`
	FilePages uint64 `json:"file_pages,string"`
	FileBytes uint64 `json:"file_bytes,string"`
	PageSize  uint64 `json:"page_size,string"`
}
type Filter struct {
	IndexID *uint64 `json:"index_id,string"`
	Level   *uint16 `json:"level,string"`
}
type Validation struct {
	Allocation        bool `json:"allocation"`
	UsedPageIntegrity bool `json:"used_page_integrity"`
	TreeNavigation    bool `json:"tree_navigation"`
	RowValues         bool `json:"row_values"`
}

// Report contains a typed slice in Groups: Summary, PageType, Index, Level,
// Segment or Region according to View. Counts exclude the full Pages/Raw data.
// Complete certifies analysis and aggregation; callers must also check output.
type Report struct {
	SchemaVersion    int        `json:"schema_version"`
	View             string     `json:"view"`
	Scope            string     `json:"scope"`
	Complete         bool       `json:"complete"`
	Source           Source     `json:"source"`
	Filter           Filter     `json:"filter"`
	Validation       Validation `json:"validation"`
	Groups           any        `json:"groups"`
	PhysicalReads    uint64     `json:"-"`
	TraversalEntries uint64     `json:"-"`
}
type Summary struct {
	FilePages          uint64 `json:"file_pages,string"`
	FileBytes          uint64 `json:"file_bytes,string"`
	UsedPages          uint64 `json:"used_pages,string"`
	FreePages          uint64 `json:"free_pages,string"`
	UninitializedPages uint64 `json:"uninitialized_pages,string"`
	TailPages          uint64 `json:"tail_pages,string"`
	ZeroPages          uint64 `json:"zero_pages,string"`
	IndexCount         uint64 `json:"index_count,string"`
	SegmentCount       uint64 `json:"segment_count,string"`
	UnknownUsedPages   uint64 `json:"unknown_used_pages,string"`
	UsedShare          *Ratio `json:"used_share"`
}
type PageType struct {
	TypeCode         uint16 `json:"type_code,string"`
	TypeName         string `json:"type_name"`
	Pages            uint64 `json:"pages,string"`
	Bytes            uint64 `json:"bytes,string"`
	ShareOfUsedPages *Ratio `json:"share_of_used_pages"`
}
type Index struct {
	IndexID           uint64 `json:"index_id,string"`
	Kind              string `json:"kind"`
	RootPage          uint32 `json:"root_page,string"`
	Height            uint32 `json:"height,string"`
	Pages             uint64 `json:"pages,string"`
	LeafPages         uint64 `json:"leaf_pages,string"`
	InternalPages     uint64 `json:"internal_pages,string"`
	LeafLinkedRecords uint64 `json:"leaf_linked_records,string"`
	LeafDeleteMarked  uint64 `json:"leaf_delete_marked_records,string"`
	LeafNonDeleted    uint64 `json:"leaf_non_deleted_records,string"`
	LOBAllocatedPages uint64 `json:"lob_allocated_pages,string"`
}
type Level struct {
	IndexID             uint64 `json:"index_id,string"`
	Level               uint16 `json:"level,string"`
	Pages               uint64 `json:"pages,string"`
	LinkedRecords       uint64 `json:"linked_records,string"`
	DeleteMarkedRecords uint64 `json:"delete_marked_records,string"`
	FreeListRecords     uint64 `json:"free_list_records,string"`
	HeapBytes           uint64 `json:"heap_bytes,string"`
	LayoutUsedBytes     uint64 `json:"layout_used_bytes,string"`
	GarbageBytes        uint64 `json:"garbage_bytes,string"`
	ContiguousFreeBytes uint64 `json:"contiguous_free_bytes,string"`
	DirectoryBytes      uint64 `json:"directory_bytes,string"`
	LayoutOccupancy     *Ratio `json:"layout_occupancy"`
	LayoutOccupancyMin  *Ratio `json:"layout_occupancy_min"`
	LayoutOccupancyMax  *Ratio `json:"layout_occupancy_max"`
}
type Segment struct {
	SegmentID             uint64  `json:"segment_id,string"`
	IndexID               *uint64 `json:"index_id,string"`
	Role                  string  `json:"role"`
	UsedPages             uint64  `json:"used_pages,string"`
	ReservedPages         uint64  `json:"reserved_pages,string"`
	ReservedUnusedPages   uint64  `json:"reserved_unused_pages,string"`
	FragmentPageCount     uint64  `json:"fragment_page_count,string"`
	FreeExtentCount       uint64  `json:"free_extent_count,string"`
	PartialExtentCount    uint64  `json:"partial_extent_count,string"`
	FullExtentCount       uint64  `json:"full_extent_count,string"`
	AllocationUtilization *Ratio  `json:"allocation_utilization"`
}
type Region struct {
	FirstPage  uint64  `json:"first_page,string"`
	EndPage    uint64  `json:"end_page,string"`
	Pages      uint64  `json:"pages,string"`
	Allocation string  `json:"allocation"`
	TypeCode   *uint16 `json:"type_code,string"`
	IndexID    *uint64 `json:"index_id,string"`
	SegmentID  *uint64 `json:"segment_id,string"`
}

// Build runs AnalyzeSpace exactly once and returns nil on any failure, including
// missing requested groups. The caller owns the reader and the stable snapshot.
func Build(ctx context.Context, r io.ReaderAt, size int64, o Options) (*Report, error) {
	if ctx == nil || r == nil {
		return nil, fmt.Errorf("%w: nil stats context/reader", innodb.ErrUnsupported)
	}
	if err := Validate(o); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	space, err := innodb.AnalyzeSpace(ctx, r, size, o.Space)
	if err != nil {
		return nil, err
	}
	return aggregate(ctx, space, o)
}

// aggregate only receives a complete AnalyzeSpace report; it never decodes pages.
func aggregate(ctx context.Context, s *innodb.SpaceReport, o Options) (*Report, error) {
	if o.View == "" {
		o.View = "summary"
	}
	r := &Report{
		SchemaVersion: 1, View: o.View, Scope: "allocation_and_local_index_layout",
		Source:        Source{s.SpaceID, s.FilePages, s.FilePages * innodb.PageSize, innodb.PageSize},
		Validation:    Validation{Allocation: true, UsedPageIntegrity: true},
		PhysicalReads: s.PhysicalReads, TraversalEntries: s.TraversalEntries,
	}
	// Own filter values so callers cannot change a report by reusing Options.
	if o.IndexID != nil {
		id := *o.IndexID
		r.Filter.IndexID = &id
		found := false
		for _, idx := range s.Indexes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if idx.ID == id {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("stats: index ID %d not found", id)
		}
	}
	if o.Level != nil {
		level := *o.Level
		r.Filter.Level = &level
	}
	selected := func(id uint64) bool { return o.IndexID == nil || id == *o.IndexID }
	switch o.View {
	case "summary":
		g := Summary{
			FilePages: s.FilePages, FileBytes: r.Source.FileBytes, UsedPages: s.UsedPages,
			FreePages: s.FreePages, UninitializedPages: s.UninitializedPages, TailPages: s.TailPages,
			ZeroPages: s.ZeroPages, IndexCount: uint64(len(s.Indexes)), SegmentCount: uint64(len(s.Segments)),
			UsedShare: ratio(s.UsedPages, s.FilePages),
		}
		for _, p := range s.Pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if p.Allocation == "used" && len(p.Raw) != 0 {
				g.UnknownUsedPages++
			}
		}
		r.Groups = []Summary{g}
	case "page-types":
		groups := []PageType{}
		for code, count := range s.UsedPageTypes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			groups = append(groups, PageType{code, typeName(code), count, count * innodb.PageSize, ratio(count, s.UsedPages)})
		}
		sort.Slice(groups, func(i, j int) bool {
			if groups[i].Pages != groups[j].Pages {
				return groups[i].Pages > groups[j].Pages
			}
			return groups[i].TypeCode < groups[j].TypeCode
		})
		r.Groups = groups
	case "indexes":
		deleted := map[uint64]uint64{}
		for _, p := range s.Pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if p.Allocation == "used" && p.Index != nil && p.Index.Level == 0 {
				deleted[p.Index.IndexID] += uint64(p.Index.DeletedRecords)
			}
		}
		groups := []Index{}
		for _, idx := range s.Indexes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !selected(idx.ID) {
				continue
			}
			groups = append(groups, Index{idx.ID, typeName(s.Pages[idx.Root].Type), idx.Root, idx.Height,
				idx.Pages, idx.LeafPages, idx.Pages - idx.LeafPages, idx.PhysicalRecords,
				deleted[idx.ID], idx.PhysicalRecords - deleted[idx.ID], idx.LOBPages})
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].IndexID < groups[j].IndexID })
		r.Groups = groups
	case "levels":
		type key struct {
			id    uint64
			level uint16
		}
		byLevel := map[key]*Level{}
		for _, p := range s.Pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			x := p.Index
			if p.Allocation != "used" || x == nil || !selected(x.IndexID) || o.Level != nil && x.Level != *o.Level {
				continue
			}
			k := key{x.IndexID, x.Level}
			g := byLevel[k]
			if g == nil {
				g = &Level{IndexID: k.id, Level: k.level}
				byLevel[k] = g
			}
			layout := uint64(x.LayoutUsedBytes)
			if g.Pages == 0 || layout < g.LayoutOccupancyMin.Numerator {
				g.LayoutOccupancyMin = ratio(layout, innodb.PageSize)
			}
			if g.Pages == 0 || layout > g.LayoutOccupancyMax.Numerator {
				g.LayoutOccupancyMax = ratio(layout, innodb.PageSize)
			}
			g.Pages++
			g.LinkedRecords += uint64(x.Records)
			g.DeleteMarkedRecords += uint64(x.DeletedRecords)
			g.FreeListRecords += uint64(x.FreeRecords)
			g.HeapBytes += uint64(x.HeapBytes)
			g.LayoutUsedBytes += layout
			g.GarbageBytes += uint64(x.GarbageBytes)
			g.ContiguousFreeBytes += uint64(x.ContiguousFreeBytes)
			g.DirectoryBytes += uint64(x.DirectoryBytes)
		}
		groups := []Level{}
		for _, g := range byLevel {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			g.LayoutOccupancy = ratio(g.LayoutUsedBytes, g.Pages*innodb.PageSize)
			groups = append(groups, *g)
		}
		if o.Level != nil && len(groups) == 0 {
			return nil, fmt.Errorf("stats: index ID %d level %d not found", *o.IndexID, *o.Level)
		}
		sort.Slice(groups, func(i, j int) bool {
			if groups[i].IndexID != groups[j].IndexID {
				return groups[i].IndexID < groups[j].IndexID
			}
			return groups[i].Level < groups[j].Level
		})
		r.Groups = groups
	case "segments":
		groups := []Segment{}
		for _, seg := range s.Segments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !selected(seg.IndexID) {
				continue
			}
			role := seg.Role
			if role == "" {
				role = "unassigned"
			}
			groups = append(groups, Segment{seg.ID, nonzero(seg.IndexID), role, seg.UsedPages, seg.ReservedPages,
				seg.ReservedPages - seg.UsedPages, uint64(len(seg.FragmentPages)), uint64(len(seg.FreeExtents)),
				uint64(len(seg.PartialExtents)), uint64(len(seg.FullExtents)), ratio(seg.UsedPages, seg.ReservedPages)})
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].SegmentID < groups[j].SegmentID })
		r.Groups = groups
	case "regions":
		owners := map[uint64]uint64{}
		for _, seg := range s.Segments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			owners[seg.ID] = seg.IndexID
		}
		groups := []Region{}
		for _, p := range s.Pages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			g := Region{FirstPage: uint64(p.Number), EndPage: uint64(p.Number) + 1, Pages: 1, Allocation: p.Allocation}
			if p.Allocation == "used" {
				code := p.Type
				g.TypeCode = &code
				g.SegmentID = nonzero(p.SegmentID)
				id := owners[p.SegmentID]
				if p.Index != nil {
					id = p.Index.IndexID
				}
				g.IndexID = nonzero(id)
			}
			if len(groups) != 0 {
				last := &groups[len(groups)-1]
				if last.EndPage == g.FirstPage && last.Allocation == g.Allocation && equal(last.TypeCode, g.TypeCode) && equal(last.IndexID, g.IndexID) && equal(last.SegmentID, g.SegmentID) {
					last.EndPage = g.EndPage
					last.Pages++
					continue
				}
			}
			groups = append(groups, g)
		}
		r.Groups = groups
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.Complete = true
	return r, nil
}

func nonzero(n uint64) *uint64 {
	if n == 0 {
		return nil
	}
	return &n
}
func equal[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func typeName(code uint16) string {
	switch code {
	case 3:
		return "INODE"
	case 5:
		return "IBUF_BITMAP"
	case 8:
		return "FSP_HDR"
	case 9:
		return "XDES"
	case 10:
		return "BLOB"
	case 18:
		return "SDI_BLOB"
	case 22:
		return "LOB_INDEX"
	case 23:
		return "LOB_DATA"
	case 24:
		return "LOB_FIRST"
	case 17853:
		return "SDI"
	case 17855:
		return "INDEX"
	default:
		return "UNKNOWN"
	}
}
