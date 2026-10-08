package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"innodb-go-reader/rowio"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

type partitionManifest struct {
	Version int `json:"version"`
	Files   []struct {
		Partition string `json:"partition"`
		Path      string `json:"path"`
	} `json:"files"`
}
type manifestError struct{ err error }

func (e *manifestError) Error() string { return "partition manifest: " + e.err.Error() }
func (e *manifestError) Unwrap() error { return e.err }
func readManifest(path string) (partitionManifest, error) {
	var m partitionManifest
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return m, err
	}
	bad := func(s string) (partitionManifest, error) { return m, &manifestError{fmt.Errorf("%s", s)} }
	if len(b) > 1<<20 || !utf8.Valid(b) {
		return bad("expected UTF-8 object, at most 1 MiB")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return bad("expected UTF-8 object, at most 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, &manifestError{err}
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return bad("expected one object")
	}
	if m.Version != 1 || len(m.Files) == 0 || len(m.Files) > innodb.MaxPartitionFiles {
		return bad("unsupported version or file count")
	}
	names := map[string]bool{}
	for i, item := range m.Files {
		if item.Partition == "" || item.Path == "" || names[item.Partition] {
			return bad("empty/duplicate partition or path")
		}
		names[item.Partition] = true
		if !filepath.IsAbs(item.Path) {
			m.Files[i].Path = filepath.Join(filepath.Dir(path), item.Path)
		}
	}
	return m, nil
}
func executePartitions(ctx context.Context, c config, stdout io.Writer, s *summary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m, err := readManifest(c.manifest)
	if err != nil {
		return err
	}
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	inputs := make([]innodb.PartitionInput, 0, len(m.Files))
	protected := []string{c.manifest}
	for _, entry := range m.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		f, e := os.Open(entry.Path)
		if e != nil {
			return e
		}
		opened = append(opened, f)
		st, e := f.Stat()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("partition input must be a regular stable snapshot")
		}
		inputs = append(inputs, innodb.PartitionInput{Name: entry.Partition, Reader: f, Size: st.Size()})
		protected = append(protected, entry.Path)
	}
	return output(ctx, c.output, c.overwrite, protected, stdout, func(w io.Writer) error {
		if c.command == "metadata" {
			set, err := innodb.InspectPartitions(ctx, inputs, c.scan)
			if err != nil {
				return err
			}
			return json.NewEncoder(w).Encode(set)
		}
		scan := innodb.ScanPartitions
		if c.materialized {
			scan = innodb.ScanPartitionsMaterialized
		}
		var enc *rowio.Encoder
		report, err := scan(ctx, inputs, c.scan, func(e innodb.PartitionEvent) error {
			if c.command == "check" {
				return nil
			}
			if e.Metadata != nil {
				var err error
				enc, err = rowio.NewEncoder(w, c.format, rowio.Header{Version: rowio.Version, Columns: e.Metadata.Columns, VirtualColumns: e.Metadata.VirtualColumns, Physical: true}, rowio.Options{})
				return err
			}
			if e.Event.Record == nil {
				return nil
			}
			provenance := struct {
				Partition *innodb.PartitionSource `json:"partition"`
				Record    json.RawMessage         `json:"record,omitempty"`
			}{Partition: e.Source}
			var err error
			if c.physical {
				provenance.Record, err = physicalJSON(e.Event.Record)
				if err != nil {
					return err
				}
			}
			b, err := json.Marshal(provenance)
			if err != nil {
				return err
			}
			return enc.Write(rowio.Row{Values: e.Event.Record.Values, Physical: b})
		})
		s.Report = report
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if c.command == "check" {
			return json.NewEncoder(w).Encode(report)
		}
		return enc.Finish()
	})
}
