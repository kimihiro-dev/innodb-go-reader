package innodb

import (
	"context"
	"fmt"
	"io"
)

// LOBBlock owns its Data. Offset is within the complete value (including prefix).
// Prefix blocks have no Chunk source; their bytes come from source.Prefix.
// Other blocks identify the underlying page/offset and, for new LOB, index entry.
// Blocks are raw InnoDB bytes and may split UTF-8 or binary JSON structures.
type LOBBlock struct {
	Offset uint64
	Prefix bool
	Chunk  LOBChunk
	Data   []byte
}

type LOBReport struct {
	Complete                                              bool
	Bytes, Blocks                                         uint64 // delivered, including the callback that returned an error
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64
}

// StreamLOB visits a current raw external value without materializing or decoding
// it. Only source.Reference and source.Prefix are inputs; derived fields are ignored.
// The caller supplies a trusted reference/prefix from the same stable snapshot.
// It need not first read the complete row. This does not validate the column type,
// the reference's record ownership, or MVCC visibility. The source is not modified.
// Errors cannot retract prior blocks. Only nil error and Complete certify the chain.
func StreamLOB(ctx context.Context, r io.ReaderAt, size int64, source ExternalField, options ScanOptions, yield func(LOBBlock) error) (report LOBReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil LOB callback", ErrUnsupported)
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
	if len(source.Prefix) != 0 && len(source.Prefix) != compactPrefixBytes {
		return report, fmt.Errorf("%w: LOB prefix length", ErrUnsupported)
	}
	field, err := parseExternal(source.Reference[:], uint64(^uint32(0)), be.Uint32(source.Reference[:]))
	if err != nil {
		return report, err
	}
	field.Prefix = append([]byte(nil), source.Prefix...)
	if uint64(field.Length)+uint64(len(field.Prefix)) > reader.options.MaxLOBBytes {
		return report, fmt.Errorf("%w: raw LOB bytes", ErrLimit)
	}
	err = walkExternal(reader, size, &field, func(b []byte, chunk LOBChunk, prefix bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		block := LOBBlock{Offset: report.Bytes, Prefix: prefix, Chunk: chunk, Data: append([]byte(nil), b...)}
		report.Bytes += uint64(len(b))
		report.Blocks++
		if err := yield(block); err != nil {
			return err
		}
		return ctx.Err()
	})
	report.Complete = err == nil
	return report, err
}
