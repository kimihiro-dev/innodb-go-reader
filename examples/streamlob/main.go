// Write raw LOB bytes to stdout and the completion report to stderr. A failing
// exit status means stdout may contain only a prefix. Input references are trusted.
package main

import (
	"context"
	"encoding/hex"
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
	if len(os.Args) < 3 || len(os.Args) > 4 {
		return fmt.Errorf("usage: streamlob file.ibd reference-hex [prefix-hex]")
	}
	ref, err := hex.DecodeString(os.Args[2])
	if err != nil || len(ref) != 20 {
		return fmt.Errorf("reference must contain exactly 20 bytes in hex")
	}
	source := innodb.ExternalField{}
	copy(source.Reference[:], ref)
	if len(os.Args) == 4 {
		source.Prefix, err = hex.DecodeString(os.Args[3])
		if err != nil {
			return err
		}
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
	report, err := innodb.StreamLOB(ctx, f, st.Size(), source, innodb.ScanOptions{}, func(block innodb.LOBBlock) error { _, e := os.Stdout.Write(block.Data); return e })
	if e := json.NewEncoder(os.Stderr).Encode(report); e != nil {
		return e
	}
	return err
}
