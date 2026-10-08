// Secondary prints the physical index model, not complete table rows.
package main

import (
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: secondary file.ibd index-name")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	result, err := innodb.ReadSecondaryAuto(f, info.Size(), os.Args[2])
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
