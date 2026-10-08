// Report physical state of a stable snapshot after controlled changes.
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
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: changes table.ibd")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	result, err := innodb.ReadAuto(f, stat.Size())
	if err != nil {
		return err
	}
	report := struct {
		Rows, DeleteMarked, Pages, FreePages, GarbageBytes int
		RootLevel                                          uint16
	}{Rows: len(result.Records), DeleteMarked: len(result.DeletedRecords), Pages: len(result.Pages), RootLevel: result.Page.Level}
	for _, page := range result.Pages {
		if page.Free != 0 {
			report.FreePages++
		}
		report.GarbageBytes += int(page.Garbage)
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
