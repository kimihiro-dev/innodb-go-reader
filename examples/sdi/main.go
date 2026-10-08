// sdi extracts raw SDI objects as JSON Lines. Input must be an uncompressed file.
package main

import (
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sdi table.ibd")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	result, err := innodb.ReadSDI(f, stat.Size())
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	for _, r := range result.Records {
		if err := enc.Encode(struct {
			Type   uint32          `json:"type"`
			ID     uint64          `json:"id"`
			Object json.RawMessage `json:"object"`
		}{r.Key.Type, r.Key.ID, r.JSON}); err != nil {
			return err
		}
	}
	return nil
}
