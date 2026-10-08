// Emit JSON events followed by an explicit completion/error report. stdout may
// already contain a prefix when exit status is nonzero; always check the report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	innodb "innodb-go-reader"
	"os"
	"os/signal"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	materialized := flag.Bool("materialized", false, "explicitly read only stored columns")
	maxRows := flag.Uint64("max-rows", 0, "row budget; zero uses default")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: scan [-materialized] [-max-rows N] file.ibd")
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	enc := json.NewEncoder(os.Stdout)
	yield := func(event innodb.ScanEvent) error {
		return enc.Encode(struct {
			Event innodb.ScanEvent `json:"event"`
		}{event})
	}
	var report innodb.ScanReport
	options := innodb.ScanOptions{MaxRows: *maxRows}
	if *materialized {
		report, err = innodb.ScanMaterializedAuto(ctx, f, st.Size(), options, yield)
	} else {
		report, err = innodb.ScanAuto(ctx, f, st.Size(), options, yield)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if e := enc.Encode(struct {
		Report innodb.ScanReport `json:"report"`
		Error  string            `json:"error,omitempty"`
	}{report, message}); e != nil {
		return e
	}
	return err
}
