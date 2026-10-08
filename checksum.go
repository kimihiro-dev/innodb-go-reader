package innodb

import (
	"bytes"
	"fmt"
	"hash/crc32"
)

var pageCRCTable = crc32.MakeTable(crc32.Castagnoli)

// pageCRC follows InnoDB's two independently checksummed regions, not a
// checksum of their concatenation. The caller supplies one complete 16 KiB page.
func pageCRC(b []byte) uint32 {
	return crc32.Checksum(b[4:26], pageCRCTable) ^ crc32.Checksum(b[38:PageSize-8], pageCRCTable)
}

func verifyPageChecksum(b []byte) error {
	if bytes.Count(b, []byte{0}) == PageSize {
		return fmt.Errorf("%w: referenced page is all zero (uninitialized or damaged)", ErrCorrupt)
	}
	if be.Uint32(b[20:24]) != be.Uint32(b[PageSize-4:]) {
		return fmt.Errorf("%w: header/trailer LSN low 32 bits differ", ErrCorrupt)
	}
	want := pageCRC(b)
	head, tail := be.Uint32(b[:4]), be.Uint32(b[PageSize-8:PageSize-4])
	if head != want || tail != want {
		return fmt.Errorf("%w: CRC32C mismatch: header=%08x trailer=%08x calculated=%08x; strict crc32 format required", ErrCorrupt, head, tail, want)
	}
	return nil
}
