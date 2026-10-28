package utp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// A write whose context ends while it waits for room reports what the
// connection took, and the connection sends exactly that.
func TestCancelledWriteReportsWhatWillBeSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.BufferSize = 64 * 1024
	_, _, client, server := restingPair(t, ctx, cfg)

	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	// Nothing reads at the server, so the windows fill and the write waits.
	wctx, wcancel := context.WithTimeout(ctx, 500*time.Millisecond)
	n, err := client.Write(wctx, payload)
	wcancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write returned %d, %v; want a deadline error", n, err)
	}
	if n == 0 || n >= len(payload) {
		t.Fatalf("write took %d of %d bytes; the test needs a partial write", n, len(payload))
	}
	// Give the connection time to take anything it was still going to.
	time.Sleep(200 * time.Millisecond)
	client.Close()

	var got []byte
	if _, err := server.ReadToEOF(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != n || !bytes.Equal(got, payload[:n]) {
		t.Fatalf("peer received %d bytes; the cancelled write reported %d", len(got), n)
	}
}
