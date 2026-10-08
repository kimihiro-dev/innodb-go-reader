// Query emits ScanEvent JSON lines and a final QueryReport. Check both completion
// and exit status: emitted prefixes cannot be retracted after a later failure.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	innodb "innodb-go-reader"
	"io"
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
	materialized := flag.Bool("materialized", false, "explicitly select stored columns")
	flag.Parse()
	if flag.NArg() != 2 {
		return fmt.Errorf("usage: query [-materialized] file.ibd query.json")
	}
	spec, err := os.Open(flag.Arg(1))
	if err != nil {
		return err
	}
	defer spec.Close()
	dec := json.NewDecoder(spec)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var query innodb.KeyRange
	if err = dec.Decode(&query); err != nil {
		return err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("query file must contain one JSON object")
	}
	// Binary key members use {"base64":"..."} so they remain distinct from text.
	keys := []innodb.Key{query.Prefix}
	if query.Lower != nil {
		keys = append(keys, query.Lower.Key)
	}
	if query.Upper != nil {
		keys = append(keys, query.Upper.Key)
	}
	for _, key := range keys {
		for i, v := range key {
			if obj, ok := v.(map[string]any); ok {
				text, ok := obj["base64"].(string)
				if !ok || len(obj) != 1 {
					return fmt.Errorf("binary keys require a single base64 property")
				}
				b, err := base64.StdEncoding.DecodeString(text)
				if err != nil {
					return err
				}
				key[i] = b
			}
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
	enc := json.NewEncoder(os.Stdout)
	yield := func(event innodb.ScanEvent) error {
		return enc.Encode(struct {
			Event innodb.ScanEvent `json:"event"`
		}{event})
	}
	var report innodb.QueryReport
	if *materialized {
		report, err = innodb.QueryMaterializedAuto(ctx, f, st.Size(), query, innodb.ScanOptions{}, yield)
	} else {
		report, err = innodb.QueryAuto(ctx, f, st.Size(), query, innodb.ScanOptions{}, yield)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if e := enc.Encode(struct {
		Report innodb.QueryReport `json:"report"`
		Error  string             `json:"error,omitempty"`
	}{report, message}); e != nil {
		return e
	}
	return err
}
