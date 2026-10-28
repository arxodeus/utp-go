package utp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// A read waiting behind another ends when its own context does.
func TestAReadWaitingForAnotherHonoursItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, client, server := restingPair(t, ctx, NewConnectionConfig())

	// Nothing has been sent, so this one waits, holding the read lock.
	first := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := server.Read(context.Background(), buf)
		first <- buf[:n]
	}()
	time.Sleep(300 * time.Millisecond)

	done, cancelled := context.WithCancel(ctx)
	cancelled()
	if _, err := server.Read(done, make([]byte, 64)); !errors.Is(err, context.Canceled) {
		t.Fatalf("a read with a cancelled context returned %v, want context.Canceled", err)
	}

	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	start := time.Now()
	var all []byte
	_, err := server.ReadToEOF(short, &all)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("a ReadToEOF whose context expired while it waited returned %v after %v", err, time.Since(start))
	}

	// The first read is unaffected, and gets what is sent.
	if _, err := client.Write(ctx, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-first:
		if !bytes.Equal(got, []byte("hello")) {
			t.Fatalf("the first read got %q, want %q", got, "hello")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first read never returned")
	}
}

// A read waiting for another gives up when the read side is closed.
func TestAReadWaitingForAnotherEndsAtCloseRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, _, server := restingPair(t, ctx, NewConnectionConfig())

	go func() { _, _ = server.Read(context.Background(), make([]byte, 64)) }()
	time.Sleep(300 * time.Millisecond)
	second := make(chan error, 1)
	go func() {
		_, err := server.Read(context.Background(), make([]byte, 64))
		second <- err
	}()
	time.Sleep(100 * time.Millisecond)
	server.CloseRead()
	select {
	case err := <-second:
		if !errors.Is(err, ErrReadClosed) {
			t.Fatalf("the waiting read returned %v, want ErrReadClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read waiting for another ignored CloseRead")
	}
}
