package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

func readSchema(path string) (innodb.Schema, error) {
	var schema innodb.Schema
	err := readDefinition(path, &schema, '{', "schema")
	return schema, err
}

func readIndexes(path string) ([]innodb.SecondarySchema, error) {
	var indexes []innodb.SecondarySchema
	err := readDefinition(path, &indexes, '[', "indexes")
	return indexes, err
}

func readDefinition(path string, value any, opener byte, label string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return fmt.Errorf("%s exceeds 1 MiB", label)
	}
	if !utf8.Valid(b) {
		return fmt.Errorf("%s must be UTF-8", label)
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != opener {
		return fmt.Errorf("%s must start with %c", label, opener)
	}
	keys := json.NewDecoder(bytes.NewReader(b))
	keys.UseNumber()
	if err = uniqueSchemaValue(keys, 0); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s must contain one JSON value", label)
	}
	return nil
}

// Include nested objects and case aliases accepted by encoding/json. Explicit
// null is ambiguous for physical attributes; optional fields should be omitted.
func uniqueSchemaValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("schema exceeds 64 nesting levels")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("schema attributes must not be null")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			for _, r := range name {
				if r > 127 {
					return fmt.Errorf("schema field names must be ASCII")
				}
			}
			name = strings.ToLower(name)
			if seen[name] {
				return fmt.Errorf("duplicate schema key %q", key)
			}
			seen[name] = true
		}
		if err := uniqueSchemaValue(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func tableSchema(r io.ReaderAt, size int64, c config) (*innodb.Schema, error) {
	if c.schema != "" {
		schema, err := readSchema(c.schema)
		if err != nil {
			return nil, err
		}
		if err = innodb.ValidateSchema(r, size, schema); err != nil {
			return nil, err
		}
		if len(schema.VirtualColumns) > 0 && !c.materialized && c.command != "metadata" && !(c.command == "check" && c.scope == "secondary") {
			return nil, fmt.Errorf("%w: VIRTUAL columns require --materialized", innodb.ErrUnsupported)
		}
		return &schema, nil
	}
	meta, err := innodb.InspectTable(r, size)
	if err != nil {
		if strings.Contains(err.Error(), "SDI flag is absent") {
			return nil, fmt.Errorf("%w; supply trusted metadata with --schema for MySQL 5.7", err)
		}
		return nil, err
	}
	schema := meta.Schema
	if c.materialized {
		schema = meta.MaterializedSchema
	}
	if schema == nil {
		return nil, fmt.Errorf("%w: %v", innodb.ErrUnsupported, meta.Issues)
	}
	return schema, nil
}

func secondarySchemas(r io.ReaderAt, size int64, table innodb.Schema, path string) ([]innodb.SecondarySchema, error) {
	indexes, err := readIndexes(path)
	if err != nil {
		return nil, err
	}
	if err = innodb.ValidateSecondarySchemas(r, size, table, indexes); err != nil {
		return nil, err
	}
	return indexes, nil
}

func selectSecondary(indexes []innodb.SecondarySchema, name string) (innodb.SecondarySchema, error) {
	for _, index := range indexes {
		if index.Name == name {
			return index, nil
		}
	}
	return innodb.SecondarySchema{}, fmt.Errorf("%w: unknown secondary index %q", innodb.ErrUnsupported, name)
}
