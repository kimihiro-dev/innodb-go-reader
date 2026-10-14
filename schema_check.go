package innodb

import (
	"fmt"
	"io"
)

// ValidateSchema checks trusted schema attributes, page 0 and the supplied
// clustered root's identity and local layout. It does not certify descendants,
// column values, transaction visibility or the schema's SQL provenance.
// VIRTUAL definitions are allowed; complete-row readers still reject them.
func ValidateSchema(r io.ReaderAt, size int64, schema Schema) error {
	if _, err := schema.validate(); err != nil {
		return err
	}
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return err
	}
	if err = checkTablespace(fsp, schema); err != nil {
		return err
	}
	root, err := readPage(r, size, schema.RootPage)
	if err != nil {
		return err
	}
	page, err := parseIndex(root, schema)
	if err != nil {
		return err
	}
	if page.Previous != ^uint32(0) || page.Next != ^uint32(0) {
		return fmt.Errorf("%w: supplied clustered root has siblings", ErrCorrupt)
	}
	return nil
}

// ValidateSecondarySchemas checks supplied definitions against the table and
// each other, then checks page 0 and each root's local layout and identity.
// It does not prove that the list is exhaustive or validate descendant pages.
// The clustered root is checked separately by ValidateSchema.
func ValidateSecondarySchemas(r io.ReaderAt, size int64, table Schema, indexes []SecondarySchema) error {
	if _, err := table.validate(); err != nil {
		return err
	}
	clusterName := "PRIMARY"
	if table.ClusteredKey != nil {
		clusterName = table.ClusteredKey.Name
	}
	names := map[string]bool{clusterName: true}
	ids := map[uint64]bool{table.IndexID: true}
	roots := map[uint32]bool{table.RootPage: true}
	for _, index := range indexes {
		if err := validateSecondaryTable(table, index); err != nil {
			return fmt.Errorf("index %q: %w", index.Name, err)
		}
		if names[index.Name] || ids[index.IndexID] || roots[index.RootPage] {
			return fmt.Errorf("%w: duplicate index identity %q", ErrUnsupported, index.Name)
		}
		names[index.Name], ids[index.IndexID], roots[index.RootPage] = true, true, true
	}
	if len(indexes) == 0 {
		return nil
	}
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return err
	}
	if err = checkTablespace(fsp, table); err != nil {
		return err
	}
	for _, index := range indexes {
		root, err := readPage(r, size, index.RootPage)
		if err != nil {
			return err
		}
		page, err := parseIndexMinimum(root, Schema{RowFormat: index.RowFormat, SpaceID: index.SpaceID, IndexID: index.IndexID}, 6)
		if err != nil {
			return fmt.Errorf("index %q: %w", index.Name, err)
		}
		if page.Previous != ^uint32(0) || page.Next != ^uint32(0) {
			return fmt.Errorf("%w: supplied secondary root %q has siblings", ErrCorrupt, index.Name)
		}
	}
	return nil
}
