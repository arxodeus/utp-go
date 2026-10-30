//go:build cgo

package libutp_test

import (
	"sync"
	"testing"

	"github.com/arxodeus/utp/internal/integration/libutp"
)

// The driver's clock can be read after the driver is closed, and from another
// goroutine while it moves. A connection given it as its clock reads it from
// its own goroutine, and goes on after its test has closed the driver; that
// read went to freed memory and crashed the fuzzer's workers.
func TestDriverClockOutlivesTheDriver(t *testing.T) {
	d, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = d.Now()
		}
	}()
	for i := 0; i < 1000; i++ {
		d.Advance(1)
	}
	wg.Wait()
	at := d.Now()
	d.Close()
	if got := d.Now(); got != at {
		t.Fatalf("the clock reads %d after Close, want %d, the time it was closed at", got, at)
	}
}
