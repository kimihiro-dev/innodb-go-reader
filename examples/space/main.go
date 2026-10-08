// Space emits a complete allocation report or exits unsuccessfully.
package main

import (
	"context"
	"encoding/json"
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
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: space file.ibd")
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	report, err := innodb.AnalyzeSpace(ctx, f, st.Size(), innodb.SpaceOptions{})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
