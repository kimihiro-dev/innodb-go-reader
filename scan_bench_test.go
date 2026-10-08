package innodb

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
)

func BenchmarkTableScan(b *testing.B) {
	data := scanFixture(b, "testdata/cluster/rowid_deep.ibd.gz")
	schema := loadScanSchema(b, "testdata/cluster/rowid_deep.json")
	r := bytes.NewReader(data)
	size := int64(len(data))
	b.Run("Read", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			result, err := Read(r, size, schema)
			if err != nil {
				b.Fatal(err)
			}
			runtime.KeepAlive(result)
		}
	})
	b.Run("Scan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			report, err := Scan(context.Background(), r, size, schema, ScanOptions{}, func(ScanEvent) error { return nil })
			if err != nil || !report.Complete {
				b.Fatal(err)
			}
		}
	})
}

// This opt-in experiment samples live heap after GC, not RSS or total allocation.
// Forced collections perturb timing; use BenchmarkTableScan for allocation/time.
func TestScanMemory(t *testing.T) {
	if os.Getenv("INNODB_SCAN_MEMORY") != "1" {
		t.Skip("set INNODB_SCAN_MEMORY=1 for sampled live-heap experiment")
	}
	data := scanFixture(t, "testdata/cluster/rowid_deep.ibd.gz")
	schema := loadScanSchema(t, "testdata/cluster/rowid_deep.json")
	r := bytes.NewReader(data)
	size := int64(len(data))
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	base := m.HeapAlloc
	atomic, err := Read(r, size, schema)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&m)
	atomicLive := m.HeapAlloc - base
	rows := len(atomic.Records)
	runtime.KeepAlive(atomic)
	atomic = nil
	t.Logf("Read: %d retained rows, %d live heap bytes above baseline", rows, atomicLive)
	for _, stop := range []uint64{1000, 0} {
		runtime.GC()
		runtime.ReadMemStats(&m)
		base = m.HeapAlloc
		peak := uint64(0)
		count := uint64(0)
		report, err := Scan(context.Background(), r, size, schema, ScanOptions{}, func(e ScanEvent) error {
			if e.Record == nil {
				return nil
			}
			count++
			if count == 1 || count%128 == 0 || count == stop || count == uint64(rows) {
				runtime.GC()
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > base && m.HeapAlloc-base > peak {
					peak = m.HeapAlloc - base
				}
			}
			if stop != 0 && count == stop {
				return ErrStopped
			}
			return nil
		})
		if stop == 0 {
			if err != nil || !report.Complete {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrStopped) || report.Complete {
			t.Fatal(err)
		}
		if peak >= atomicLive/2 {
			t.Fatalf("stream retains too much: %d vs %d", peak, atomicLive)
		}
		t.Logf("Scan: %d delivered rows, sampled peak %d live heap bytes above baseline, %d page requests", count, peak, report.PageReads)
	}
	runtime.KeepAlive(data)
}
