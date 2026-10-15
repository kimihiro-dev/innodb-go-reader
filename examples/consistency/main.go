// Consistency checks selected indexes under independently recorded export evidence.
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
	materialized := flag.Bool("materialized", false, "report non-indexed VIRTUAL definitions separately")
	flag.Parse()
	if flag.NArg() != 4 {
		return fmt.Errorf("usage: consistency [-materialized] table.ibd schema.json indexes.json evidence.json")
	}
	var table innodb.Schema
	var indexes []innodb.SecondarySchema
	var evidence innodb.ExportSnapshotEvidence
	for i, value := range []any{&table, &indexes, &evidence} {
		raw, err := os.ReadFile(flag.Arg(i + 1))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, value); err != nil {
			return err
		}
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
	check := innodb.CheckIndexConsistency
	if *materialized {
		check = innodb.CheckIndexConsistencyMaterialized
	}
	p, err := check(ctx, f, st.Size(), table, indexes, innodb.ConsistencyOptions{Evidence: &evidence})
	if e := json.NewEncoder(os.Stdout).Encode(p); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	if !p.Passed {
		return fmt.Errorf("index consistency not certified: preconditions=%s, issues=%d", p.Preconditions.State, len(p.Issues))
	}
	return nil
}
