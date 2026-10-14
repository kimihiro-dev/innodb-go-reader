package cli

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"os"
	"path/filepath"
)

func executeTrustedManifest(ctx context.Context, c config, stdout io.Writer, s *summary) error {
	var definition innodb.PartitionSchema
	if err := readDefinition(c.partitionSchema, &definition, '{', "partition schema"); err != nil {
		return err
	}
	if err := innodb.ValidatePartitionSchema(definition); err != nil {
		return err
	}
	limit := c.scan.MaxPageReads
	if limit == 0 {
		limit = 1000000
	}
	budget := &budgetReader{ctx: ctx, max: limit}
	defer func() { s.PreflightReads = budget.reads }()
	spaces := map[uint32]string{}
	for _, p := range definition.Partitions {
		spaces[p.Schema.SpaceID] = p.Name
	}
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	inputs := make([]innodb.PartitionInput, 0, len(c.paths))
	paths := map[string]string{}
	for _, path := range c.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		opened = append(opened, f)
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("partition input must be a regular stable snapshot")
		}
		r := manifestReader{file: f, budget: budget}
		var page [innodb.PageSize]byte
		if _, err := r.ReadAt(page[:], 0); err != nil {
			return err
		}
		name := spaces[binary.BigEndian.Uint32(page[34:])]
		if name == "" || paths[name] != "" {
			return fmt.Errorf("%w: unknown/duplicate partition space", innodb.ErrCorrupt)
		}
		paths[name] = path
		inputs = append(inputs, innodb.PartitionInput{Name: name, Reader: r, Size: st.Size()})
	}
	set, err := innodb.InspectPartitionsWithSchema(ctx, inputs, definition, c.scan)
	if err != nil {
		return err
	}
	pathForManifest := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if c.output != "" {
			base, err := filepath.Abs(filepath.Dir(c.output))
			if err != nil {
				return "", err
			}
			if relative, err := filepath.Rel(base, absolute); err == nil {
				return relative, nil
			}
		}
		return absolute, nil
	}
	manifest := partitionManifest{Version: 2}
	manifest.Schema, err = pathForManifest(c.partitionSchema)
	if err != nil {
		return err
	}
	for _, part := range set.Partitions {
		path, err := pathForManifest(paths[part.Source.Name])
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, struct {
			Partition string `json:"partition"`
			Path      string `json:"path"`
		}{part.Source.Name, path})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 1<<20 {
		return fmt.Errorf("generated manifest exceeds 1 MiB")
	}
	s.Report = struct {
		Partitions int    `json:"partitions"`
		Database   string `json:"database"`
		Table      string `json:"table"`
	}{len(set.Partitions), set.Database, set.Name}
	protected := append(append([]string(nil), c.paths...), c.partitionSchema)
	return output(ctx, c.output, c.overwrite, protected, stdout, func(w io.Writer) error { _, err := w.Write(append(data, '\n')); return err })
}
