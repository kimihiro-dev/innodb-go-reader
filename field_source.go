package innodb

import (
	"io"
	"sort"
)

// ByteSpan is a page-relative, half-open byte range. An empty stored value has
// Start == End; a missing span means that no such physical bytes exist.
type ByteSpan struct {
	Start, End int
}

// NullBit identifies one bit in the record's NULL bitmap, whether set or clear.
type NullBit struct {
	Offset int
	Mask   byte
}

// FieldSource describes a stored, default, dropped, or system field. Column is
// the current table column index, or -1 for dropped/system fields. Position is
// the actual record field ordinal including system fields, or -1 for defaults.
// Reference and Prefix are subranges of Data, never additional payload bytes.
type FieldSource struct {
	Column, Position int
	Name, Kind       string
	Null             bool
	Data             *ByteSpan       `json:",omitempty"`
	Length           *ByteSpan       `json:",omitempty"`
	NullBit          *NullBit        `json:",omitempty"`
	Prefix           *ByteSpan       `json:",omitempty"`
	Reference        *ByteSpan       `json:",omitempty"`
	Default          *InstantDefault `json:",omitempty"`
}

// RecordSource is matched to a local record/node by its origin Offset. Fields
// follow physical order, then synthesized defaults in current column order.
type RecordSource struct {
	Offset             int
	Node, DeleteMarked bool
	Fields             []FieldSource
	RowVersion         *ByteSpan `json:",omitempty"`
	FieldCount         *ByteSpan `json:",omitempty"`
}

// TraceClusteredPage has ReadClusteredPage's validation and I/O scope, and also
// collects field provenance and separate VIRTUAL definitions. It reads no LOB.
// External-marked payloads, including dropped/deleted fields, must fit a reference.
func TraceClusteredPage(r io.ReaderAt, size int64, number uint32, schema Schema) (*ClusteredPage, error) {
	return readClusteredPage(r, size, number, schema, true)
}

// TraceSecondaryPage collects physical field provenance with the same local
// validation and I/O scope as ReadSecondaryPage, including locator/prefix copies.
func TraceSecondaryPage(r io.ReaderAt, size int64, number uint32, schema SecondarySchema) (*SecondaryPage, error) {
	return readSecondaryPage(r, size, number, schema, true)
}

func systemSource(name string, position, start, end int) FieldSource {
	return FieldSource{Column: -1, Position: position, Name: name, Kind: "system", Data: &ByteSpan{start, end}}
}

func defaultSource(column int, name string, d InstantDefault) FieldSource {
	d.Data = append([]byte(nil), d.Data...)
	return FieldSource{Column: column, Position: -1, Name: name, Kind: "default", Null: d.Null, Default: &d}
}

func orderedSource(s *RecordSource) RecordSource {
	sort.Slice(s.Fields, func(i, j int) bool {
		a, b := s.Fields[i], s.Fields[j]
		if a.Position == b.Position {
			return a.Column < b.Column
		}
		if a.Position < 0 {
			return false
		}
		if b.Position < 0 {
			return true
		}
		return a.Position < b.Position
	})
	return *s
}
