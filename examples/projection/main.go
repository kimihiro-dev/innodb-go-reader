// Projection emits selected clustered columns and a final completion report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	innodb "github.com/kimihiro-dev/innodb-go-reader"
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
	materialized := flag.Bool("materialized", false, "explicit stored-column view; report VIRTUAL definitions separately")
	flag.Parse()
	if flag.NArg() < 3 {
		return fmt.Errorf("usage: projection [-materialized] table.ibd schema.json column [column ...]")
	}
	raw, err := os.ReadFile(flag.Arg(1))
	if err != nil {
		return err
	}
	var schema innodb.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
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
	yield := func(e innodb.ProjectedEvent) error {
		if e.Record == nil {
			return nil
		}
		return enc.Encode(struct {
			Row *innodb.ProjectedRecord `json:"row"`
		}{e.Record})
	}
	var report innodb.ScanReport
	if *materialized {
		report, err = innodb.ScanProjectedMaterialized(ctx, f, st.Size(), schema, flag.Args()[2:], innodb.ScanOptions{}, yield)
	} else {
		report, err = innodb.ScanProjected(ctx, f, st.Size(), schema, flag.Args()[2:], innodb.ScanOptions{}, yield)
	}
	if e := enc.Encode(struct {
		Report innodb.ScanReport `json:"report"`
	}{report}); e != nil {
		return e
	}
	return err
}
