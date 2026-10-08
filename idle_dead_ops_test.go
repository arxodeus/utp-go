package utp_go

import (
	"context"
	"errors"
	"testing"
	"time"
)

// deadByIdleTimeout returns an accepted stream whose connection the idle
// timeout has closed: the peer sends a SYN and one data packet, then nothing
// for longer than MaxIdleTimeout.
func deadByIdleTimeout(t *testing.T) (*UtpStream, context.CancelFunc, func()) {
	t.Helper()
	const (
		ourSeq  = 0x4321
		peerID  = 6000
		peerSeq = 900
		idle    = 10 * time.Second
	)
	clk := newVirtualClock(time.Unix(0, 0).Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	restore := pinRandom(ourSeq)

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }
	cfg.MaxIdleTimeout = idle

	cid := NewConnectionId(conn.peer, peerID+1, peerID)
	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sock.AcceptWithCid(ctx, cid, cfg)
		if err != nil {
			t.Errorf("accept: %v", err)
		}
		accepted <- s
	}()
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	stream := <-accepted
	if stream == nil {
		t.FailNow()
	}
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 110000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("hello")).Build().Encode())
	})
	buf := make([]byte, 16)
	if n, err := stream.Read(ctx, buf); err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("first read: %q, %v", buf[:n], err)
	}
	// Silence, past the idle timeout, in steps so each timer fires when due.
	for elapsed := time.Duration(0); elapsed < idle+time.Second; elapsed += 500 * time.Millisecond {
		clk.Advance(500 * time.Millisecond)
	}
	clk.AwaitQuiet()
	return stream, cancel, func() {
		sock.Close()
		cancel()
		restore()
	}
}

// A write on a connection the idle timeout has closed fails at once, with the
// reason, rather than waiting for its own deadline.
func TestWriteAfterIdleTimeoutFails(t *testing.T) {
	stream, _, done := deadByIdleTimeout(t)
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := stream.Write(ctx, []byte("into the void"))
	took := time.Since(start)
	t.Logf("write after idle timeout: err=%v after %v", err, took)
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("write after the idle timeout returned %v after %v, want ErrTimedOut at once", err, took)
	}
}

// A read on a connection the idle timeout has closed reports the timeout, not
// a clean end of stream -- also once the socket's context has been cancelled
// as well. Read then had two ready cases, the closed read queue carrying the
// error and the cancelled stream context mapped to io.EOF, and Go picks
// between them at random, so this repeats.
func TestReadAfterIdleTimeoutReportsIt(t *testing.T) {
	for i := 0; i < 10; i++ {
		stream, cancelSocket, done := deadByIdleTimeout(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		buf := make([]byte, 16)
		_, first := stream.Read(ctx, buf)
		cancelSocket()
		<-stream.streamCtx.Done()
		// A fresh stream's Read, and ReadToEOF, after the socket went too.
		var all []byte
		_, toEOF := stream.ReadToEOF(ctx, &all)
		cancel()
		done()
		if !errors.Is(first, ErrTimedOut) || !errors.Is(toEOF, ErrTimedOut) {
			t.Fatalf("run %d: Read returned %v, ReadToEOF after the socket's context was cancelled %v; want ErrTimedOut from both",
				i, first, toEOF)
		}
	}
	// Read's cached error does not exercise its own select a second time,
	// so Read is checked on a stream whose socket went first.
	for i := 0; i < 10; i++ {
		stream, cancelSocket, done := deadByIdleTimeout(t)
		cancelSocket()
		<-stream.streamCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		buf := make([]byte, 16)
		_, err := stream.Read(ctx, buf)
		cancel()
		done()
		if !errors.Is(err, ErrTimedOut) {
			t.Fatalf("run %d: Read after the idle timeout and the socket's context returned %v, want ErrTimedOut", i, err)
		}
	}
}
