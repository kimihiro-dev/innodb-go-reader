package cli

import (
	"context"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"innodb-go-reader/internal/mysql57"
	"io"
	"os"
	"path/filepath"
)

func executeSchema(ctx context.Context, c config, stdout io.Writer, s *summary) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	dest, err := filepath.Abs(c.output)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dest); err == nil {
		return fmt.Errorf("schema output directory already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".innodb-schema-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	bundle, err := mysql57.Collect(ctx, c.collect)
	if err != nil {
		return err
	}
	if err = mysql57.WriteDefinitions(stage, bundle, c.collect.Indexes); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Mkdir reserves the destination without replacing a concurrently created directory.
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	var created []string
	defer func() {
		if err != nil {
			for _, name := range created {
				os.Remove(name)
			}
			os.Remove(dest)
		}
	}()
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	paths := map[string]string{"directory": dest}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		name := filepath.Join(dest, entry.Name())
		// Link publishes each complete file without overwriting an existing child.
		if err = os.Link(filepath.Join(stage, entry.Name()), name); err != nil {
			return err
		}
		created = append(created, name)
		paths[entry.Name()] = name
	}
	s.Report = paths
	err = json.NewEncoder(contextWriter{ctx, stdout}).Encode(paths)
	return err
}

// All physical reads, including the second collection preflight, share this budget.
type manifestReader struct {
	file   *os.File
	budget *budgetReader
}

func (r manifestReader) ReadAt(p []byte, off int64) (int, error) {
	r.budget.r = r.file
	return r.budget.ReadAt(p, off)
}

func executeManifest(ctx context.Context, c config, stdout io.Writer, s *summary) error {
	if c.partitionSchema != "" {
		return executeTrustedManifest(ctx, c, stdout, s)
	}
	limit := c.scan.MaxPageReads
	if limit == 0 {
		limit = 1000000
	}
	budget := &budgetReader{ctx: ctx, max: limit}
	defer func() { s.PreflightReads = budget.reads }()
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	inputs := make([]innodb.PartitionInput, len(c.paths))
	spaces := make([]string, len(c.paths))
	var directory []struct {
		Name    string `json:"name"`
		Indexes []struct {
			Space string `json:"tablespace_ref"`
		} `json:"indexes"`
	}
	tables := 0
	totalSDI := 0
	for i, path := range c.paths {
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
		reader := manifestReader{file: f, budget: budget}
		inputs[i] = innodb.PartitionInput{Reader: reader, Size: st.Size()}
		sdi, err := innodb.ReadSDI(reader, st.Size())
		if err != nil {
			return fmt.Errorf("%s SDI: %w", path, err)
		}
		for _, record := range sdi.Records {
			if len(record.JSON) > innodb.MaxSDITotalBytes-totalSDI {
				return fmt.Errorf("%w: collection SDI bytes", innodb.ErrLimit)
			}
			totalSDI += len(record.JSON)
			var envelope struct {
				Type   string          `json:"dd_object_type"`
				Object json.RawMessage `json:"dd_object"`
			}
			if err = json.Unmarshal(record.JSON, &envelope); err != nil {
				return err
			}
			switch envelope.Type {
			case "Tablespace":
				if spaces[i] != "" {
					return fmt.Errorf("multiple tablespaces in %s", path)
				}
				var space struct {
					Name string `json:"name"`
				}
				if err = json.Unmarshal(envelope.Object, &space); err != nil {
					return err
				}
				spaces[i] = space.Name
			case "Table":
				tables++
				if tables > 1 {
					return fmt.Errorf("manifest requires exactly one logical Table SDI")
				}
				var table struct {
					Partitions json.RawMessage `json:"partitions"`
				}
				if err = json.Unmarshal(envelope.Object, &table); err != nil {
					return err
				}
				if err = json.Unmarshal(table.Partitions, &directory); err != nil {
					return err
				}
				if len(directory) > innodb.MaxPartitionFiles {
					return fmt.Errorf("%w: partition directory count", innodb.ErrLimit)
				}
			}
		}
	}
	if tables != 1 || len(directory) == 0 {
		return fmt.Errorf("manifest requires one complete partition Table SDI")
	}
	names := map[string]string{}
	for _, part := range directory {
		if part.Name == "" || len(part.Indexes) == 0 {
			return fmt.Errorf("invalid partition directory")
		}
		for _, idx := range part.Indexes {
			if idx.Space == "" || names[idx.Space] != "" && names[idx.Space] != part.Name {
				return fmt.Errorf("ambiguous partition tablespace")
			}
			names[idx.Space] = part.Name
		}
	}
	pathsByName := map[string]string{}
	for i := range inputs {
		inputs[i].Name = names[spaces[i]]
		if inputs[i].Name == "" {
			return fmt.Errorf("file %s is absent from partition directory", c.paths[i])
		}
		pathsByName[inputs[i].Name] = c.paths[i]
	}
	set, err := innodb.InspectPartitions(ctx, inputs, c.scan)
	if err != nil {
		return err
	}
	manifest := partitionManifest{Version: 1}
	for _, part := range set.Partitions {
		path, err := filepath.Abs(pathsByName[part.Source.Name])
		if err != nil {
			return err
		}
		if c.output != "" {
			base, err := filepath.Abs(filepath.Dir(c.output))
			if err != nil {
				return err
			}
			if relative, e := filepath.Rel(base, path); e == nil {
				path = relative
			}
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
	return output(ctx, c.output, c.overwrite, c.paths, stdout, func(w io.Writer) error { _, err := w.Write(append(data, '\n')); return err })
}
