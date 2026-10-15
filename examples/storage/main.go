// Storage emits exact column/record storage counts for one stable table file.
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
	materialized := flag.Bool("materialized", false, "report VIRTUAL definitions separately; analyze stored columns only")
	flag.Parse()
	if flag.NArg() != 2 {
		return fmt.Errorf("usage: storage [-materialized] table.ibd schema.json")
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
	analyze := innodb.AnalyzeStorage
	if *materialized {
		analyze = innodb.AnalyzeStorageMaterialized
	}
	report, err := analyze(ctx, f, st.Size(), schema, innodb.ScanOptions{})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
