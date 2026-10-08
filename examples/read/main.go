// A small reproducible example, not a general SQL/CLI frontend.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	innodb "innodb-go-reader"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: go run ./examples/read file.ibd schema.json")
	}
	meta, err := os.Open(os.Args[2])
	if err != nil {
		return err
	}
	defer meta.Close()
	var schema innodb.Schema
	decoder := json.NewDecoder(meta)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return err
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	result, err := innodb.Read(f, info.Size(), schema)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, r := range result.Records {
		if err := encoder.Encode(r.Values); err != nil {
			return err
		}
	}
	return nil
}
