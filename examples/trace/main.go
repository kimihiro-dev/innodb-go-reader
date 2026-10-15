// Trace physical field sources on one clustered index page without reading LOBs.
package main

import (
	"encoding/json"
	"fmt"
	innodb "github.com/kimihiro-dev/innodb-go-reader"
	"os"
	"strconv"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: trace table.ibd schema.json page-number")
	}
	number, err := strconv.ParseUint(os.Args[3], 10, 32)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	var schema innodb.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	page, err := innodb.TraceClusteredPage(f, st.Size(), uint32(number), schema)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(page)
}
