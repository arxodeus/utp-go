package utp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// A write waiting behind another one ends when its own context does. The
// writes were serialised by a mutex, so one queued behind a writer the peer's
// window held up waited however long that took. From the review of 294cc2e
// (finding 1); the already-cancelled case is that review's proposed test.
func TestAWriteWaitingForAnotherHonoursItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.BufferSize = 64 * 1024
	_, _, client, server := restingPair(t, ctx, cfg)

	// The peer is not reading, so this one waits, holding the write lock.
	first := make(chan error, 1)
	big := bytes.Repeat([]byte{1}, 4<<20)
	go func() {
		_, err := client.Write(context.Background(), big)
		first <- err
	}()
	time.Sleep(300 * time.Millisecond)

	done, cancelled := context.WithCancel(ctx)
	cancelled()
	if _, err := client.Write(done, []byte("second")); !errors.Is(err, context.Canceled) {
		t.Fatalf("a write with a cancelled context returned %v, want context.Canceled", err)
	}

	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	start := time.Now()
	_, err := client.WriteV(short, [][]byte{[]byte("third")})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("a write whose context expired while it waited returned %v after %v", err, time.Since(start))
	}

	// The first write is unaffected, and the others sent nothing.
	got := make(chan []byte, 1)
	go func() {
		var b []byte
		server.ReadToEOF(ctx, &b)
		got <- b
	}()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	client.Close()
	if b := <-got; !bytes.Equal(b, big) {
		t.Fatalf("peer received %d bytes, want the first write's %d", len(b), len(big))
	}
}
