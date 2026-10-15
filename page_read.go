package innodb

import (
	"fmt"
	"io"
	"strings"
)

// PageHeader contains only FIL fields, valid independently of the page type.
type PageHeader struct {
	Number, SpaceID, Previous, Next uint32
	Type                            uint16
	LSN                             uint64
	Checksum                        uint32
	Trailer                         [8]byte
}

// PhysicalPage owns one verified page's bytes. Verification covers CRC32C,
// trailer LSN and the requested page number, not allocation or internal layout.
type PhysicalPage struct {
	Header PageHeader
	Raw    []byte
}

// LocalRecord is a clustered leaf record with local values only. It is a distinct
// type from a complete Record: Values contains ExternalField for each unresolved
// off-page value, and nil only for SQL NULL. Columns refer to Schema.Columns;
// VIRTUAL columns are absent. External retains the same unresolved references.
// Reference format/chunks are determined only when StreamLOB reads the chain.
type LocalRecord Record

// ClusteredPage contains only the selected page. Leaf records and delete-mark
// evidence are separate; non-leaf pages contain Nodes instead. No LOB is read.
type ClusteredPage struct {
	Page           Page
	Records        []LocalRecord
	DeletedRecords []DeletedRecord
	Nodes          []NodePointer
	Sources        []RecordSource  `json:",omitempty"` // Populated only by TraceClusteredPage.
	VirtualColumns []VirtualColumn `json:",omitempty"` // Separate definitions, only when tracing.
}

// SecondaryPage contains local physical index entries, never complete table rows.
// Unique keys are checked within this page only, excluding deleted/NULL keys.
type SecondaryPage struct {
	Page                    Page
	Records, DeletedRecords []SecondaryRecord
	Nodes                   []SecondaryNode
	Sources                 []RecordSource `json:",omitempty"` // Populated only by TraceSecondaryPage.
}

// ReadRawPage copies exactly one page from a stable, aligned 16 KiB file.
// It does not check any contents, including checksum, page number or page type.
// The caller owns the reader and result. Failure returns nil with its I/O cause.
func ReadRawPage(r io.ReaderAt, size int64, number uint32) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil page reader", ErrUnsupported)
	}
	offset := int64(number) * PageSize
	if size < PageSize || size%PageSize != 0 || offset > size-PageSize {
		return nil, fmt.Errorf("%w: page %d outside aligned file of %d bytes", ErrCorrupt, number, size)
	}
	b := make([]byte, PageSize)
	n, err := r.ReadAt(b, offset)
	if err != nil && !(n == len(b) && err == io.EOF) {
		return nil, fmt.Errorf("read page %d at file offset %d: %w", number, offset, err)
	}
	if n != len(b) {
		return nil, fmt.Errorf("read page %d: %w", number, io.ErrUnexpectedEOF)
	}
	return b, nil
}

// ReadPage reads just the requested page, verifying its strict crc32 envelope.
// Unknown page types retain their common header and bytes without interpretation.
// This does not read page 0 or establish the tablespace's supported format.
func ReadPage(r io.ReaderAt, size int64, number uint32) (*PhysicalPage, error) {
	b, err := readPage(r, size, number)
	if err != nil {
		return nil, err
	}
	h := PageHeader{
		Number: be.Uint32(b[4:8]), SpaceID: be.Uint32(b[34:38]),
		Previous: be.Uint32(b[8:12]), Next: be.Uint32(b[12:16]),
		Type: be.Uint16(b[24:26]), LSN: be.Uint64(b[16:24]), Checksum: be.Uint32(b[:4]),
	}
	copy(h.Trailer[:], b[PageSize-8:])
	return &PhysicalPage{Header: h, Raw: b}, nil
}

// ReadClusteredPage validates page 0 and one selected clustered INDEX page using
// trusted schema. It reads no SDI, root (unless selected), sibling, child or LOB.
// Local success does not certify allocation, the whole tree, or MVCC visibility.
// Any failure returns nil. Results own their byte/value data.
func ReadClusteredPage(r io.ReaderAt, size int64, number uint32, schema Schema) (*ClusteredPage, error) {
	return readClusteredPage(r, size, number, schema, false)
}

func readClusteredPage(r io.ReaderAt, size int64, number uint32, schema Schema, capture bool) (*ClusteredPage, error) {
	pk, err := schema.validate()
	if err != nil {
		return nil, err
	}
	b, p, err := readLocalIndexPage(r, size, number, schema, 10)
	if err != nil {
		return nil, err
	}
	entries, err := decodePageSources(b, p, schema, pk, capture)
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", number, err)
	}
	result := &ClusteredPage{Page: p, Records: make([]LocalRecord, 0)}
	if capture {
		result.VirtualColumns = append([]VirtualColumn(nil), schema.VirtualColumns...)
	}
	for _, e := range entries {
		if capture {
			result.Sources = append(result.Sources, orderedSource(e.source))
			e.source = nil
		}
		if p.Level > 0 {
			if err := checkLocalChild(e.child, number, size); err != nil {
				return nil, err
			}
			result.Nodes = append(result.Nodes, NodePointer{PageNumber: number, Start: e.Start, Offset: e.Offset, End: e.End, Header: e.Header, Key: nodeKey(e.Record, pk), ChildPage: e.child, Minimum: e.minimum})
			continue
		}
		if e.deleteMarked {
			for _, i := range pk {
				if schema.Columns[i].Type == "CHAR" {
					e.Values[i] = strings.TrimRight(e.Values[i].(string), " ")
				}
			}
			result.DeletedRecords = append(result.DeletedRecords, DeletedRecord{PageNumber: number, Start: e.Start, Offset: e.Offset, End: e.End, Header: e.Header, Key: nodeKey(e.Record, pk), RowID: e.RowID, Transaction: e.Transaction, RollPointer: e.RollPointer, Raw: append([]byte{}, b[e.Start:e.End]...)})
			continue
		}
		for i, c := range schema.Columns {
			if c.Type == "CHAR" && e.Values[i] != nil {
				if e.CharStorage == nil {
					e.CharStorage = make(map[int]string)
				}
				e.CharStorage[i] = e.Values[i].(string)
				e.Values[i] = strings.TrimRight(e.Values[i].(string), " ")
			}
		}
		for _, field := range e.External {
			e.Values[field.Column] = field
		}
		result.Records = append(result.Records, LocalRecord(e.Record))
	}
	return result, nil
}

// ReadSecondaryPage validates page 0 and one ordinary secondary INDEX page using
// trusted schema. No other index pages or clustered rows are read. Cross-page
// order/uniqueness and secondary/clustered correspondence remain unverified.
func ReadSecondaryPage(r io.ReaderAt, size int64, number uint32, schema SecondarySchema) (*SecondaryPage, error) {
	return readSecondaryPage(r, size, number, schema, false)
}

func readSecondaryPage(r io.ReaderAt, size int64, number uint32, schema SecondarySchema, capture bool) (*SecondaryPage, error) {
	columns, err := schema.validate()
	if err != nil {
		return nil, err
	}
	identity := Schema{SpaceID: schema.SpaceID, IndexID: schema.IndexID, RowFormat: schema.RowFormat}
	b, p, err := readLocalIndexPage(r, size, number, identity, 6)
	if err != nil {
		return nil, err
	}
	entries, err := decodePageEntries(b, p, func(origin, limit int) (pageEntry, error) {
		return decodeSecondarySources(b, origin, limit, columns, schema.Fields, p.Level > 0, capture)
	}, func(a, b pageEntry) bool { return compareSecondary(a.order, b.order, columns) < 0 })
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", number, err)
	}
	result := &SecondaryPage{Page: p, Records: make([]SecondaryRecord, 0)}
	var unique indexKey
	for _, e := range entries {
		if capture {
			result.Sources = append(result.Sources, orderedSource(e.source))
			e.source = nil
		}
		record := secondaryRecord(e, b, schema)
		if p.Level > 0 {
			if err := checkLocalChild(e.child, number, size); err != nil {
				return nil, err
			}
			result.Nodes = append(result.Nodes, SecondaryNode{SecondaryRecord: record, ChildPage: e.child, Minimum: e.minimum})
		} else if e.deleteMarked {
			result.DeletedRecords = append(result.DeletedRecords, record)
		} else {
			if schema.Unique {
				hasNull := false
				for _, v := range e.order[:schema.UserFields] {
					hasNull = hasNull || v == nil
				}
				if !hasNull {
					if unique != nil && compareSecondary(unique, e.order, columns[:schema.UserFields]) == 0 {
						return nil, fmt.Errorf("%w: page %d duplicate live unique secondary key", ErrCorrupt, number)
					}
					unique = e.order
				}
			}
			result.Records = append(result.Records, record)
		}
	}
	return result, nil
}

func readLocalIndexPage(r io.ReaderAt, size int64, number uint32, identity Schema, minimum int) ([]byte, Page, error) {
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return nil, Page{}, err
	}
	if err := checkTablespace(fsp, identity); err != nil {
		return nil, Page{}, err
	}
	b := fsp
	if number != 0 {
		b, err = readPage(r, size, number)
		if err != nil {
			return nil, Page{}, err
		}
	}
	p, err := parseIndexMinimum(b, identity, minimum)
	if err != nil {
		return nil, Page{}, err
	}
	if int64(p.Level) >= size/PageSize || p.Level > 0 && p.Records == 0 {
		return nil, Page{}, fmt.Errorf("%w: page %d impossible level or empty internal page", ErrCorrupt, number)
	}
	return b, p, nil
}

func checkLocalChild(child, page uint32, size int64) error {
	if child == 0 || child == page || int64(child) >= size/PageSize {
		return fmt.Errorf("%w: page %d invalid child %d", ErrCorrupt, page, child)
	}
	return nil
}
