package innodb

import (
	"fmt"
	"io"
	"strings"
)

// MaterializedResult explicitly describes a stored-column view, not a full SQL row.
// Values and every column index in Result refer to Columns, in SQL declaration order
// with VIRTUAL columns excluded. INVISIBLE and STORED generated columns are included.
type MaterializedResult struct {
	Columns        []Column
	VirtualColumns []VirtualColumn // Unmaterialized; no placeholder or SQL NULL is returned.
	Result         *Result
}

// ReadMaterialized reads every stored column using trusted schema. It never evaluates
// expressions or reads secondary-index materializations. Failure returns nil.
func ReadMaterialized(r io.ReaderAt, size int64, schema Schema) (*MaterializedResult, error) {
	if _, err := schema.validate(); err != nil {
		return nil, err
	}
	virtual := schema.VirtualColumns
	schema.VirtualColumns = nil
	result, err := Read(r, size, schema)
	if err != nil {
		return nil, err
	}
	return &MaterializedResult{Columns: schema.Columns, VirtualColumns: virtual, Result: result}, nil
}

// ReadMaterializedAuto explicitly opts into the stored-column view discovered by SDI.
func ReadMaterializedAuto(r io.ReaderAt, size int64) (*MaterializedResult, error) {
	m, err := InspectTable(r, size)
	if err != nil {
		return nil, err
	}
	if m.MaterializedSchema == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
	}
	return ReadMaterialized(r, size, *m.MaterializedSchema)
}
