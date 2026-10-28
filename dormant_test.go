package utp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"runtime/pprof"
	"testing"
	"time"
)

// restingPair is two connected streams on two loopback sockets.
func restingPair(t *testing.T, ctx context.Context, cfg *ConnectionConfig) (sa, sb *Socket, client, server *Stream) {
	t.Helper()
	lg := duplexQuietLog()
	var err error
	sa, err = Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sa.Close() })
	sb, err = Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb.Close() })
	const cid = 4700
	accepted := make(chan *Stream, 1)
	go func() {
		s, _ := sb.AcceptWithCID(ctx, NewConnectionID(NewUDPPeer(sa.LocalAddr().(*net.UDPAddr)), cid+1, cid), cfg)
		accepted <- s
	}()
	client, err = sa.ConnectWithCID(ctx, NewConnectionID(NewUDPPeer(sb.LocalAddr().(*net.UDPAddr)), cid, cid+1), cfg)
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	return sa, sb, client, server
}

// exchange writes n bytes from one stream and reads them at the other.
func exchange(t *testing.T, ctx context.Context, from, to *Stream, n int) {
	t.Helper()
	if _, err := from.Write(ctx, make([]byte, n)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	for got := 0; got < n; {
		k, err := to.Read(ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		got += k
	}
}

// awaitRest waits for both streams' connections to have let their loops go.
func awaitRest(t *testing.T, within time.Duration, streams ...*Stream) {
	t.Helper()
	deadline := time.Now().Add(within)
	for _, s := range streams {
		for !s.conn.dormant.Load() {
			if time.Now().After(deadline) {
				t.Fatalf("an idle connection still has its event loop running %v after it went quiet", within)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// An established connection with nothing to do releases its goroutine, and
// resumes for whatever needs it next: a write at either end, in turn.
func TestIdleConnectionRests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, client, server := restingPair(t, ctx, NewConnectionConfig())
	defer client.Close()
	defer server.Close()

	exchange(t, ctx, client, server, 4096)
	awaitRest(t, 2*time.Second, client, server)
	// Woken by a write, from rest, at either end.
	exchange(t, ctx, client, server, 64*1024)
	exchange(t, ctx, server, client, 64*1024)
	awaitRest(t, 2*time.Second, client, server)
}

// The idle timeout is kept while resting: the connection ends when it falls
// due, not at the next keep-alive.
func TestRestingConnectionIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.MaxIdleTimeout = 500 * time.Millisecond
	_, _, client, server := restingPair(t, ctx, cfg)
	defer client.Close()
	defer server.Close()

	start := time.Now()
	exchange(t, ctx, client, server, 1024)
	awaitRest(t, 400*time.Millisecond, client, server)
	select {
	case <-server.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("a resting connection with a %v idle timeout was still open after %v",
			cfg.MaxIdleTimeout, time.Since(start))
	}
	if _, err := server.Read(ctx, make([]byte, 16)); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("read after the idle timeout: %v, want ErrTimedOut", err)
	}
}

// Closing the socket ends a resting connection at once.
func TestRestingConnectionEndsWithItsSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sa, _, client, server := restingPair(t, ctx, farRestConfig())
	defer server.Close()

	exchange(t, ctx, client, server, 1024)
	awaitRest(t, 2*time.Second, client, server)
	sa.Close()
	awaitEnded(t, client, "its socket's Close")
}

// Canceling the context the socket was created with ends a resting
// connection at once: there is no Close, so no event is queued.
func TestRestingConnectionEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sockCtx, sockCancel := context.WithCancel(ctx)
	defer sockCancel()
	_, _, client, server := restingPair(t, sockCtx, farRestConfig())

	exchange(t, ctx, client, server, 1024)
	awaitRest(t, 2*time.Second, client, server)
	sockCancel()
	for _, s := range []*Stream{client, server} {
		awaitEnded(t, s, "its socket's context")
	}
}

// farRestConfig keeps a resting connection's own timer beyond awaitEnded's
// wait: the idle-decay deadline follows the retransmission timeout, at
// least MinTimeout, and the keep-alive is 29 seconds away. A connection that
// ends within the wait was therefore ended by what the test did, however
// slow the host.
func farRestConfig() *ConnectionConfig {
	cfg := NewConnectionConfig()
	cfg.MinTimeout = 30 * time.Second
	return cfg
}

// awaitEnded requires s to end within 5s of what should end it at once,
// and reports the goroutines if it does not. Use with farRestConfig, so
// that nothing else can end it in that time.
func awaitEnded(t *testing.T, s *Stream, cause string) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		var stacks bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&stacks, 1)
		t.Fatalf("a resting connection outlived %s by more than 5s:\n%s", cause, stacks.String())
	}
}

// A receiver whose application stopped reading rests with data held back,
// and the application's next read wakes it to deliver that data; nothing
// else would, the sender having nothing left to send.
func TestRestingReceiverWokenByRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, client, server := restingPair(t, ctx, NewConnectionConfig())
	defer client.Close()
	defer server.Close()

	const n = 512 * 1024 // more than the read queue holds
	if _, err := client.Write(ctx, make([]byte, n)); err != nil {
		t.Fatal(err)
	}
	awaitRest(t, 5*time.Second, client, server)
	server.conn.mu.Lock()
	held := server.conn.state.RecvBuf.Pending()
	server.conn.mu.Unlock()
	if held == 0 {
		t.Fatal("the receiver holds nothing back from a full read queue; nothing to test")
	}
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	buf := make([]byte, 64*1024)
	got, err := server.Read(readCtx, buf)
	if err != nil {
		t.Fatal(err)
	}
	// Woken, and with bytes still held back it stays awake, rather than
	// being started and rested again for every read.
	time.Sleep(50 * time.Millisecond)
	if !server.conn.readsHeld.Load() {
		t.Fatal("nothing is held back after one read; nothing to test")
	}
	if server.conn.dormant.Load() {
		t.Fatal("a connection holding bytes back for its reader rested")
	}
	for got < n {
		k, err := server.Read(readCtx, buf)
		if err != nil {
			t.Fatalf("read %d of %d bytes, then: %v", got, n, err)
		}
		got += k
	}
}

// A timer the socket's reader arms on a resting connection, such as a held
// acknowledgement (ackEvery), starts the loop, which takes its expiry.
func TestTimerArmedOnRestingConnectionFires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, client, server := restingPair(t, ctx, NewConnectionConfig())
	defer client.Close()
	defer server.Close()

	exchange(t, ctx, client, server, 1024)
	// Twice: the first arm creates the timer, which wakes the loop itself
	// (kickLoop); the second, after the loop has rested again, finds it
	// created.
	for i := 0; i < 2; i++ {
		awaitRest(t, 2*time.Second, client, server)
		c := server.conn
		c.mu.Lock()
		c.armAckHold(10 * time.Millisecond)
		c.mu.Unlock()
		deadline := time.Now().Add(time.Second)
		for {
			c.mu.Lock()
			pending := c.loopTimers.ackHold.pending
			c.mu.Unlock()
			if !pending {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("arm %d: a 10ms timer armed on a resting connection had not fired 1s later", i+1)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
