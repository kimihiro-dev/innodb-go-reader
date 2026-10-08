package innodb

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"strings"
)

// PartitionInput explicitly names one physical partition. Readers and snapshots
// must remain stable for the call; the caller retains ownership of each reader.
type PartitionInput struct {
	Name   string
	Reader io.ReaderAt
	Size   int64
}

// PartitionSource distinguishes file-local page numbers, record offsets and ROW_ID.
// Number is the zero-based position in the table's partition definition.
type PartitionSource struct {
	Name    string
	Number  uint32
	TableID uint64
	SpaceID uint32
}
type PartitionMetadata struct {
	Source PartitionSource
	Table  *TableMetadata
}

// PartitionSet is a complete, preflight-validated collection, in definition order.
// Source identifies the logical DD table, not an InnoDB partition table ID.
type PartitionSet struct {
	Database, Name string
	Source         SDIKey
	PartitionType  uint32
	Expression     string // Raw SDI partition_expression; KEY stores a field list, not SQL text.
	Columns        []Column
	VirtualColumns []VirtualColumn
	Partitions     []PartitionMetadata
}

// PartitionEvent first carries Metadata alone, after all files pass preflight.
// Later events carry Source and one ScanEvent. Callback data may be retained.
type PartitionEvent struct {
	Metadata *PartitionSet
	Source   *PartitionSource
	Event    ScanEvent
}
type PartitionScanReport struct {
	Complete                                              bool
	Partitions, CompletedPartitions                       uint64
	Pages, Nodes, Records, DeletedRecords                 uint64
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64
	Columns                                               []Column
	VirtualColumns                                        []VirtualColumn
}

const MaxPartitionFiles = 1024

// InspectPartitions validates the complete directory and every physical index root.
// It does not scan user rows, evaluate partition expressions or prove MVCC consistency.
// Errors return nil, including missing or mismatched partitions.
func InspectPartitions(ctx context.Context, inputs []PartitionInput, options ScanOptions) (*PartitionSet, error) {
	reader, err := partitionReader(ctx, inputs, options)
	if err != nil {
		return nil, err
	}
	set, _, err := inspectPartitionSet(reader, inputs)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return set, nil
}

// ScanPartitions scans a complete collection in partition-definition order, then
// clustered order within each partition. There is no global key ordering.
func ScanPartitions(ctx context.Context, inputs []PartitionInput, options ScanOptions, yield func(PartitionEvent) error) (PartitionScanReport, error) {
	return scanPartitions(ctx, inputs, options, yield, false)
}

// ScanPartitionsMaterialized explicitly omits VIRTUAL values while reporting them.
func ScanPartitionsMaterialized(ctx context.Context, inputs []PartitionInput, options ScanOptions, yield func(PartitionEvent) error) (PartitionScanReport, error) {
	return scanPartitions(ctx, inputs, options, yield, true)
}
func partitionReader(ctx context.Context, inputs []PartitionInput, options ScanOptions) (*scanReader, error) {
	if len(inputs) == 0 || len(inputs) > MaxPartitionFiles {
		return nil, fmt.Errorf("%w: partition file count must be 1..%d", ErrLimit, MaxPartitionFiles)
	}
	names := map[string]bool{}
	for _, in := range inputs {
		if in.Name == "" || names[in.Name] || in.Reader == nil || in.Size < PageSize || in.Size%PageSize != 0 {
			return nil, fmt.Errorf("%w: invalid/duplicate partition input %q", ErrCorrupt, in.Name)
		}
		names[in.Name] = true
	}
	return newScanReader(ctx, inputs[0].Reader, options)
}

// A cache entry is keyed only by offset. Clear it when changing file identity;
// the one shared reader retains all cumulative counters and limits.
func selectPartition(reader *scanReader, input PartitionInput) {
	reader.source = input.Reader
	reader.cache = make(map[int64]*list.Element)
	reader.lru.Init()
}
func scanPartitions(ctx context.Context, inputs []PartitionInput, options ScanOptions, yield func(PartitionEvent) error, materialized bool) (report PartitionScanReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil partition callback", ErrUnsupported)
	}
	reader, err := partitionReader(ctx, inputs, options)
	if err != nil {
		return report, err
	}
	defer func() {
		report.PageReads = reader.requests
		report.CacheHits = reader.hits
		report.PhysicalReads = reader.reads
		report.TraversalEntries = reader.entries
	}()
	set, ordered, err := inspectPartitionSet(reader, inputs)
	if err != nil {
		return report, err
	}
	report.Partitions = uint64(len(ordered))
	report.Columns = set.Columns
	report.VirtualColumns = set.VirtualColumns
	if !materialized && len(set.VirtualColumns) > 0 {
		return report, fmt.Errorf("%w: VIRTUAL columns require ScanPartitionsMaterialized", ErrUnsupported)
	}
	// Preserve private schemas before handing independent metadata to the caller.
	schemas := make([]Schema, len(set.Partitions))
	for i, p := range set.Partitions {
		schemas[i], err = copyPartitionSchema(*p.Table.MaterializedSchema)
		if err != nil {
			return report, err
		}
		if i == 0 {
			report.VirtualColumns = schemas[i].VirtualColumns
		}
		schemas[i].VirtualColumns = nil
	}
	report.Columns = schemas[0].Columns
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if err = yield(PartitionEvent{Metadata: set}); err != nil {
		return report, err
	}
	for i, in := range ordered {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		selectPartition(reader, in.PartitionInput)
		schema := schemas[i]
		source := PartitionSource{Name: in.Name, Number: uint32(i), TableID: in.tableID, SpaceID: schema.SpaceID}
		// Source identity was captured before the first callback in inspectPartitionSet;
		// callbacks receive copies and cannot redirect traversal.
		err = walkTree(reader, in.PartitionInput.Size, schema, func(event ScanEvent) error {
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
			provenance := source
			if err := yield(PartitionEvent{Source: &provenance, Event: event}); err != nil {
				return err
			}
			return ctx.Err()
		})
		if err != nil {
			return report, fmt.Errorf("partition %s: %w", in.Name, err)
		}
		report.CompletedPartitions++
	}
	report.Complete = true
	return report, nil
}
func unsupportedPartition(m *TableMetadata) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
}
