// Read supported tables using SDI, or print the discovery report.
package main

import (
	"encoding/json"
	"flag"
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
	metadata := flag.Bool("metadata", false, "print table metadata and unsupported features instead of rows")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: go run ./examples/auto [-metadata] file.ibd")
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	if *metadata {
		report, err := innodb.InspectTable(f, info.Size())
		if err != nil {
			return err
		}
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	result, err := innodb.ReadAuto(f, info.Size())
	if err != nil {
		return err
	}
	for _, record := range result.Records {
		if err := encoder.Encode(record.Values); err != nil {
			return err
		}
	}
	return nil
}
