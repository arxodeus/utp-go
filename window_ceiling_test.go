package utp

import (
	"context"
	"testing"
	"time"
)

// streamWindow reads a live stream's congestion window and bytes in flight
// as its event loop sees them.
func streamWindow(s *UtpStream) (window, inFlight uint32) {
	c := s.conn
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.SentPackets.CongestionWindow(), c.state.SentPackets.BytesInFlight()
}

// SetWindowSize is libutp's utp_setsockopt(UTP_SNDBUF) on a live connection:
// from the call on, the window is no larger than the ceiling and nothing is
// sent past it, while the transfer goes on. go-libutp exposes it as
// Conn.SetWriteBufferLen.
func TestSetWindowSizeCapsALiveConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := writevPair(t, ctx, 4300)

	const ceiling = 16 * 1024
	if got := client.WindowSize(); got != DefaultWindowSize {
		t.Fatalf("WindowSize before the call = %d, want the configured %d", got, DefaultWindowSize)
	}

	go func() {
		buf := make([]byte, 64*1024)
		for {
			if _, err := server.Read(ctx, buf); err != nil {
				return
			}
		}
	}()
	chunk := make([]byte, 64*1024)
	write := func() {
		if _, err := client.Write(ctx, chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Let the window grow well past the ceiling first, so the call has
	// something to cut.
	deadline := time.Now().Add(20 * time.Second)
	for {
		write()
		if w, _ := streamWindow(client); w > 4*ceiling {
			break
		}
		if time.Now().After(deadline) {
			w, _ := streamWindow(client)
			t.Fatalf("window never grew past %d (at %d)", 4*ceiling, w)
		}
	}

	client.SetWindowSize(ceiling)
	if got := client.WindowSize(); got != ceiling {
		t.Fatalf("WindowSize after the call = %d, want %d", got, ceiling)
	}
	maxWindow, maxInFlight := streamWindow(client)
	if maxWindow > ceiling {
		t.Fatalf("window %d right after the call, above the ceiling %d", maxWindow, ceiling)
	}

	// Bytes already in flight when the ceiling dropped drain first; after
	// that nothing goes past it.
	for i := 0; i < 8; i++ {
		write()
	}
	maxInFlight = 0
	for i := 0; i < 32; i++ {
		write()
		w, f := streamWindow(client)
		maxWindow, maxInFlight = max(maxWindow, w), max(maxInFlight, f)
	}
	if maxWindow > ceiling || maxInFlight > ceiling {
		t.Fatalf("window reached %d and flight %d with the ceiling at %d", maxWindow, maxInFlight, ceiling)
	}
	t.Logf("with a %d-byte ceiling: window at most %d, flight at most %d", ceiling, maxWindow, maxInFlight)
}
