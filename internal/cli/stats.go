package cli

import (
	"context"
	"fmt"
	"innodb-go-reader/stats"
	"io"
	"strconv"
)

func decimalFlag(raw, name string, bits int) (uint64, error) {
	if raw == "" {
		return 0, fmt.Errorf("--%s requires a decimal integer", name)
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("--%s requires a decimal integer", name)
		}
	}
	n, err := strconv.ParseUint(raw, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("--%s: %w", name, err)
	}
	return n, nil
}

type statsReader struct {
	r     io.ReaderAt
	reads uint64
}

func (r *statsReader) ReadAt(b []byte, offset int64) (int, error) {
	r.reads++
	return r.r.ReadAt(b, offset)
}

type statsSummary struct {
	View          string       `json:"view"`
	Filter        stats.Filter `json:"filter"`
	PhysicalReads uint64       `json:"physical_reads"`
	// AnalyzeSpace returns nil on failure, so no traversal count is invented.
	TraversalEntries *uint64 `json:"traversal_entries"`
}

func executeStats(ctx context.Context, c config, reader io.ReaderAt, size int64, w io.Writer, s *summary) error {
	counted := &statsReader{r: reader}
	diag := &statsSummary{View: c.stats.View, Filter: stats.Filter{IndexID: c.stats.IndexID, Level: c.stats.Level}}
	s.Report = diag
	defer func() { diag.PhysicalReads = counted.reads }()
	o := c.stats
	o.Space = c.space
	report, err := stats.Build(ctx, counted, size, o)
	if err != nil {
		return err
	}
	diag.TraversalEntries = &report.TraversalEntries
	if c.format == "json" {
		return stats.WriteJSON(ctx, w, report)
	}
	return stats.WriteTable(ctx, w, report)
}
