package utp

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// A write larger than the send buffer is not copied whole up front: it holds
// at most one send buffer's worth beyond the send buffer.
func TestALargeWriteHoldsOneChunk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.BufferSize = 256 * 1024
	_, _, client, _ := restingPair(t, ctx, cfg)

	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	go client.Write(wctx, make([]byte, 32<<20))
	time.Sleep(500 * time.Millisecond) // the peer is not reading: the write waits

	c := client.conn
	c.mu.Lock()
	held := 0
	for _, w := range c.pendingWrites {
		held += len(w.data)
	}
	c.mu.Unlock()
	for i := 0; i < len(client.writes); i++ {
		held += cfg.BufferSize // at most a chunk each, not yet taken by the loop
	}
	if held > cfg.BufferSize {
		t.Fatalf("a waiting 32 MB write holds %d bytes outside the send buffer, want at most %d", held, cfg.BufferSize)
	}
}

// Writes of several chunks, made at once from two goroutines, each arrive
// whole: a write's chunks are not interleaved with another's.
func TestConcurrentLargeWritesAreNotInterleaved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.BufferSize = 64 * 1024
	_, _, client, server := restingPair(t, ctx, cfg)

	a := bytes.Repeat([]byte{'a'}, 1<<20)
	b := bytes.Repeat([]byte{'b'}, 1<<20)
	done := make(chan error, 2)
	go func() { _, err := client.Write(ctx, a); done <- err }()
	go func() { _, err := client.WriteV(ctx, [][]byte{b[:100], b[100:]}); done <- err }()
	go func() {
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
		client.Close()
	}()
	var got []byte
	if _, err := server.ReadToEOF(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2<<20 {
		t.Fatalf("received %d bytes, want %d", len(got), 2<<20)
	}
	first, second := got[:1<<20], got[1<<20:]
	if !(bytes.Equal(first, a) && bytes.Equal(second, b)) && !(bytes.Equal(first, b) && bytes.Equal(second, a)) {
		t.Fatal("the two writes arrived interleaved")
	}
}
