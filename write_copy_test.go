package utp_go

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// A write is copied once. Write copies the caller's bytes into a slice of its
// own, so the caller may reuse its buffer at once; the send buffer then takes
// that slice as it is (sendBuffer.Adopt). It used to copy it again, and a
// 1 MB write allocated 2 MB. libutp copies the application's bytes once
// (utp_internal.cpp:1061).
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
