// Render JSONValue columns as ordinary JSON; use examples/auto for typed output.
package main

import (
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run ./examples/json file.ibd")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	result, err := innodb.ReadAuto(f, stat.Size())
	if err != nil {
		return err
	}
	// Convert all rows before printing, preserving the no-partial-output contract.
	rows := make([][]any, len(result.Records))
	for i, r := range result.Records {
		values := append([]any(nil), r.Values...)
		for j, value := range values {
			if v, ok := value.(innodb.JSONValue); ok {
				values[j], err = v.JSON()
				if err != nil {
					return err
				}
			}
		}
		rows[i] = values
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, values := range rows {
		if err := encoder.Encode(values); err != nil {
			return err
		}
	}
	return nil
}
