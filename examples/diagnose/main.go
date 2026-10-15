// Diagnose emits a structured report, exiting nonzero on findings/interruption.
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
	skipLOB := flag.Bool("skip-lob", false, "skip LOB chain/type checks; used page envelopes are still checked")
	indexes := flag.String("indexes", "", "optional trusted SecondarySchema JSON array")
	flag.Parse()
	if flag.NArg() != 2 {
		return fmt.Errorf("usage: diagnose [-skip-lob] [-indexes indexes.json] table.ibd schema.json")
	}
	raw, err := os.ReadFile(flag.Arg(1))
	if err != nil {
		return err
	}
	var schema innodb.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	options := innodb.DiagnosticOptions{SkipLOB: *skipLOB}
	if *indexes != "" {
		raw, err := os.ReadFile(*indexes)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &options.Secondary); err != nil {
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
	report, err := innodb.Diagnose(ctx, f, st.Size(), schema, options)
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	if !report.Passed {
		return fmt.Errorf("diagnostic checks did not pass (%d problems)", len(report.Problems))
	}
	return nil
}
