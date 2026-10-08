// Explicitly read stored columns and describe unmaterialized VIRTUAL columns.
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
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run ./examples/materialized file.ibd")
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
	encoder := json.NewEncoder(os.Stdout)
	result, err := innodb.ReadMaterializedAuto(f, info.Size())
	if err != nil {
		return err
	}
	return encoder.Encode(result)
}
