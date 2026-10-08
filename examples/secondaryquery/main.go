// Secondaryquery emits projected rows and a final report as JSON lines.
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
	materialized := flag.Bool("materialized", false, "explicit stored-column view")
	maxEntries := flag.Uint64("max-entries", 0, "cumulative traversal budget (0 uses default)")
	flag.Parse()
	if flag.NArg() != 3 {
		return fmt.Errorf("usage: secondaryquery [-materialized] file.ibd index query.json")
	}
	spec, err := os.Open(flag.Arg(2))
	if err != nil {
		return err
	}
	defer spec.Close()
	dec := json.NewDecoder(spec)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var q innodb.SecondaryQuery
	if err = dec.Decode(&q); err != nil {
		return err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("query file must contain one JSON object")
	}
	keys := []innodb.Key{q.Range.Prefix}
	if q.Range.Lower != nil {
		keys = append(keys, q.Range.Lower.Key)
	}
	if q.Range.Upper != nil {
		keys = append(keys, q.Range.Upper.Key)
	}
	for _, key := range keys {
		for i, v := range key {
			if obj, ok := v.(map[string]any); ok {
				value, ok := obj["base64"].(string)
				if !ok || len(obj) != 1 {
					return fmt.Errorf("binary keys require one base64 property")
				}
				b, e := base64.StdEncoding.DecodeString(value)
				if e != nil {
					return e
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
	yield := func(row innodb.ProjectedRow) error {
		return enc.Encode(struct {
			Row innodb.ProjectedRow `json:"row"`
		}{row})
	}
	var report innodb.SecondaryQueryReport
	if *materialized {
		report, err = innodb.QuerySecondaryMaterializedAuto(ctx, f, st.Size(), flag.Arg(1), q, innodb.ScanOptions{MaxEntries: *maxEntries}, yield)
	} else {
		report, err = innodb.QuerySecondaryAuto(ctx, f, st.Size(), flag.Arg(1), q, innodb.ScanOptions{MaxEntries: *maxEntries}, yield)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if e := enc.Encode(struct {
		Report innodb.SecondaryQueryReport `json:"report"`
		Error  string                      `json:"error,omitempty"`
	}{report, message}); e != nil {
		return e
	}
	return err
}
