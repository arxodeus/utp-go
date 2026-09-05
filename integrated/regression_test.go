package integrated

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

// Regression tests for the transfer-path defects fixed in this fork.
// Each test names the defect it covers; see the root-cause notes in
// KNOWN-LIMITATIONS.md and the commit history.

func quietLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	var out int
	if _, err := fmt.Sscanf(v, "%d", &out); err != nil || out == 0 {
		return def
	}
	return out
}

// TestReaderUnblocksWhenPeerGoesAway covers the defect where a connection that
// reached ConnClosed by any path other than "remote FIN fully received" never
// delivered the end-of-stream marker, leaving ReadToEOF blocked forever.
//
// The event loop guarded the final drain with `if !c.eof()`, but eof() is true
// by definition once the state is ConnClosed, so the drain never ran.
func TestReaderUnblocksWhenPeerGoesAway(t *testing.T) {
	logger := quietLogger()
	aAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4500}
	bAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4501}
	ctx := context.Background()

	aLink, err := utp.Bind(ctx, "udp4", aAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer aLink.Close()
	bLink, err := utp.Bind(ctx, "udp4", bAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer bLink.Close()

	cfg := utp.NewConnectionConfig()
	cfg.MaxIdleTimeout = 3 * time.Second

	accCid := utp.NewConnectionId(utp.NewUdpPeer(bAddr), 201, 200)
	iniCid := utp.NewConnectionId(utp.NewUdpPeer(aAddr), 200, 201)

	readDone := make(chan int, 1)
	go func() {
		s, err := aLink.AcceptWithCid(ctx, accCid, cfg)
		if err != nil {
			t.Logf("accept: %v", err)
			readDone <- -1
			return
		}
		buf := make([]byte, 0)
		n, err := s.ReadToEOF(ctx, &buf)
		t.Logf("ReadToEOF returned n=%d err=%v", n, err)
		readDone <- n
	}()

	sendStream, err := bLink.ConnectWithCid(ctx, iniCid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := sendStream.Write(ctx, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The initiator disappears. The acceptor must not block forever.
	bLink.Close()
	// Close must also be idempotent -- this second call used to panic closing
	// an already-closed channel.
	bLink.Close()

	select {
	case <-readDone:
	case <-time.After(30 * time.Second):
		t.Fatal("ReadToEOF still blocked after the peer went away")
	}
}

// TestConcurrentTransfersScale covers the handshake defect where an acceptor
// ignored a retransmitted SYN. Nothing retransmits the SYN-ACK, so a single
// lost SYN-ACK stranded the initiator until it gave up, and its peer then
// blocked forever in ReadToEOF.
//
// It needs enough concurrency to actually drop packets. Override with
// REPRO_N / REPRO_SIZE / REPRO_DEADLINE.
func TestConcurrentTransfersScale(t *testing.T) {
	n := envInt("REPRO_N", 150)
	size := envInt("REPRO_SIZE", 200000)
	deadline := time.Duration(envInt("REPRO_DEADLINE", 120)) * time.Second

	logger := quietLogger()
	recvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4400}
	sendAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4401}
	ctx := context.Background()

	recvLink, err := utp.Bind(ctx, "udp4", recvAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer recvLink.Close()
	sendLink, err := utp.Bind(ctx, "udp4", sendAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer sendLink.Close()

	data := bytes.Repeat([]byte{0x5a}, size)
	var wg sync.WaitGroup
	results := make(chan string, n*2)

	for i := 0; i < n; i++ {
		cfg := utp.NewConnectionConfig()
		initiatorCid := uint16(100 + i*2)
		responderCid := initiatorCid + 1
		recvCid := utp.NewConnectionId(utp.NewUdpPeer(sendAddr), responderCid, initiatorCid)
		sendCid := utp.NewConnectionId(utp.NewUdpPeer(recvAddr), initiatorCid, responderCid)
		wg.Add(2)
		idx := i
		go func() {
			defer wg.Done()
			s, err := recvLink.AcceptWithCid(ctx, recvCid, cfg)
			if err != nil {
				results <- fmt.Sprintf("recv %d: accept: %v", idx, err)
				return
			}
			defer s.Close()
			buf := make([]byte, 0, size)
			got, err := s.ReadToEOF(ctx, &buf)
			if err != nil && err != io.EOF {
				results <- fmt.Sprintf("recv %d: read: %v", idx, err)
				return
			}
			if got != size {
				results <- fmt.Sprintf("recv %d: short read %d want %d", idx, got, size)
				return
			}
			if !bytes.Equal(buf, data) {
				results <- fmt.Sprintf("recv %d: payload mismatch", idx)
			}
		}()
		go func() {
			defer wg.Done()
			s, err := sendLink.ConnectWithCid(ctx, sendCid, cfg)
			if err != nil {
				results <- fmt.Sprintf("send %d: connect: %v", idx, err)
				return
			}
			defer s.Close()
			if _, err := s.Write(ctx, data); err != nil {
				results <- fmt.Sprintf("send %d: write: %v", idx, err)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatalf("%d transfers did not complete within %v", n, deadline)
	}

	close(results)
	failures := 0
	for msg := range results {
		failures++
		if failures <= 20 {
			t.Error(msg)
		}
	}
	if failures > 20 {
		t.Errorf("... and %d further failures", failures-20)
	}
}

// TestAbortedTransfersTearDown hammers the teardown paths concurrently: idle
// timeout, RESET and local close all racing. It guards the time-wheel change
// (the expiry callback used to run while holding the wheel's lock, which could
// deadlock against the connection event loop) and the socket shutdown fan-out.
func TestAbortedTransfersTearDown(t *testing.T) {
	logger := quietLogger()
	aAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4700}
	bAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4701}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aLink, err := utp.Bind(ctx, "udp4", aAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer aLink.Close()
	bLink, err := utp.Bind(ctx, "udp4", bAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer bLink.Close()

	const n = 60
	data := make([]byte, 300000)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		cfg := utp.NewConnectionConfig()
		cfg.MaxIdleTimeout = time.Duration(200+i*7) * time.Millisecond
		cfg.MaxConnAttempts = 2
		ini := uint16(100 + i*2)
		accCid := utp.NewConnectionId(utp.NewUdpPeer(bAddr), ini+1, ini)
		iniCid := utp.NewConnectionId(utp.NewUdpPeer(aAddr), ini, ini+1)
		wg.Add(2)
		idx := i
		go func() {
			defer wg.Done()
			rctx, rcancel := context.WithTimeout(ctx, time.Duration(300+idx*11)*time.Millisecond)
			defer rcancel()
			s, err := aLink.AcceptWithCid(rctx, accCid, cfg)
			if err != nil {
				return
			}
			buf := make([]byte, 0)
			_, _ = s.ReadToEOF(rctx, &buf)
			s.Close()
		}()
		go func() {
			defer wg.Done()
			wctx, wcancel := context.WithTimeout(ctx, time.Duration(250+idx*13)*time.Millisecond)
			defer wcancel()
			s, err := bLink.ConnectWithCid(wctx, iniCid, cfg)
			if err != nil {
				return
			}
			_, _ = s.Write(wctx, data)
			s.Close()
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("teardown stalled")
	}
}

// TestAcceptTimesOutWithoutSyn covers the defect where an accept that expired
// from the awaiting map was dropped silently, blocking AcceptWithCid forever
// when the caller's own context had no deadline.
func TestAcceptTimesOutWithoutSyn(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for AWAITING_CONNECTION_TIMEOUT")
	}
	logger := quietLogger()
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4800}
	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4801}
	ctx := context.Background()

	link, err := utp.Bind(ctx, "udp4", addr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()

	cid := utp.NewConnectionId(utp.NewUdpPeer(peer), 901, 900)
	done := make(chan error, 1)
	go func() {
		// context.Background(): the caller supplies no deadline of its own, so
		// only the socket's own accept expiry can unblock this.
		_, err := link.AcceptWithCid(context.Background(), cid, utp.NewConnectionConfig())
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error, got a stream")
		}
		t.Logf("AcceptWithCid returned: %v", err)
	case <-time.After(utp.AWAITING_CONNECTION_TIMEOUT + 20*time.Second):
		t.Fatal("AcceptWithCid never returned for a connection that never arrived")
	}
}

// TestConnectionIdHashFromStructLiteral covers the defect where a
// ConnectionId built as a struct literal (which the exported type invites)
// had an empty cached hash, so every such id collided with every other.
func TestConnectionIdHashFromStructLiteral(t *testing.T) {
	peer := utp.NewUdpPeer(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234})
	a := &utp.ConnectionId{Send: 100, Recv: 101, Peer: peer}
	b := &utp.ConnectionId{Send: 200, Recv: 201, Peer: peer}

	if a.Hash() == "" || b.Hash() == "" {
		t.Fatal("struct-literal ConnectionId produced an empty hash")
	}
	if a.Hash() == b.Hash() {
		t.Fatal("distinct ConnectionIds collided on the same hash")
	}
	if got, want := a.Hash(), utp.NewConnectionId(peer, 101, 100).Hash(); got != want {
		t.Fatalf("literal and constructor disagree: %s vs %s", got, want)
	}
}

// TestAcceptWithoutCidWaitsForIncomingSyn covers the defect where Accept --
// the path that takes no connection id, and the only one usable against a
// peer that picks its own -- polled once for an already-arrived SYN and
// failed outright with "no incoming conn" if none had come yet.
//
// That made it unusable for the ordinary server pattern: call Accept, then
// have a client connect. The SYN arriving a moment later was parked with
// nobody left to claim it, and the peer retried until it gave up.
//
// The same path also dereferenced the accept's connection id, which is nil
// when none was given, so it panicked as soon as it got that far.
func TestAcceptWithoutCidWaitsForIncomingSyn(t *testing.T) {
	logger := quietLogger()
	srvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5100}
	cliAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5101}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := utp.Bind(ctx, "udp4", srvAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := utp.Bind(ctx, "udp4", cliAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	payload := bytes.Repeat([]byte{0x2c}, 32*1024)
	got := make(chan int, 1)
	failed := make(chan error, 1)
	go func() {
		// Accept is called first, before any SYN exists.
		stream, err := srv.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			failed <- err
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			failed <- err
			return
		}
		got <- n
	}()

	time.Sleep(150 * time.Millisecond) // let the accept register

	cid := utp.NewConnectionId(utp.NewUdpPeer(srvAddr), 7100, 7101)
	stream, err := cli.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("connect to a listening Accept failed: %v", err)
	}
	if _, err := stream.Write(ctx, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	stream.Close()

	select {
	case n := <-got:
		if n != len(payload) {
			t.Errorf("Accept path delivered %d bytes, want %d", n, len(payload))
		}
	case err := <-failed:
		t.Fatalf("Accept path failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Accept path never completed")
	}
}

// TestCloseReturnsPromptly covers the defect where UtpStream.Close set its
// shutdown flag and then waited, without waking the connection's event loop.
//
// The loop was blocked in a select on cases the flag is not one of, so it only
// noticed once some unrelated packet or timer happened to arrive. Close took
// anywhere from about a second to the idle timeout depending on what was in
// flight -- measured at 29 seconds against a peer that had gone quiet.
func TestCloseReturnsPromptly(t *testing.T) {
	logger := quietLogger()
	aAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5200}
	bAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5201}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	aLink, err := utp.Bind(ctx, "udp4", aAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer aLink.Close()
	bLink, err := utp.Bind(ctx, "udp4", bAddr, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer bLink.Close()

	cfg := utp.NewConnectionConfig()
	accCid := utp.NewConnectionId(utp.NewUdpPeer(bAddr), 601, 600)
	iniCid := utp.NewConnectionId(utp.NewUdpPeer(aAddr), 600, 601)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		s, err := aLink.AcceptWithCid(ctx, accCid, cfg)
		if err != nil {
			return
		}
		buf := make([]byte, 0)
		_, _ = s.ReadToEOF(ctx, &buf)
		s.Close()
	}()

	stream, err := bLink.ConnectWithCid(ctx, iniCid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := stream.Write(ctx, bytes.Repeat([]byte{7}, 16*1024)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Let the transfer settle so nothing is in flight to wake the loop
	// incidentally -- that quiescent state is exactly when the defect bit.
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	stream.Close()
	elapsed := time.Since(start)
	t.Logf("Close returned in %v", elapsed.Round(time.Millisecond))

	if elapsed > 5*time.Second {
		t.Errorf("Close took %v; it should not be waiting on an unrelated timer", elapsed)
	}
	<-readDone
}
