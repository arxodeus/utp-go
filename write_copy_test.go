package utp

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// A write is copied once: Write copies the caller's bytes into its own
// slice, so the caller may reuse its buffer at once, and the send buffer
// adopts that slice (sendBuffer.Adopt).
func TestWriteCopiesOnce(t *testing.T) {
	const size = 1 << 20
	stream, _, clk, done := liveAccepted(t)
	defer done()
	payload := make([]byte, size)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clk.AwaitQuiet()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := stream.Write(ctx, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("a %d-byte write allocated %d bytes", size, allocated)
	if allocated > size+size/2 {
		t.Fatalf("a %d-byte write allocated %d bytes: copied more than once", size, allocated)
	}
}
