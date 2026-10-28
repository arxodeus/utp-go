package utp

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// Fuzzing a live connection, not just the decoder.
//
// The decoder fuzzers cover one packet at a time. This covers sequences: a
// state machine accepting a packet it should not in its current state, a
// counter that wraps, a buffer indexed by a peer-chosen sequence number.
//
// An arbitrary byte sequence is not a valid conversation, so almost any
// behavior is permitted. What is not permitted is:
//
//   - panicking, which anyone who can send a datagram could trigger;
//   - hanging, the same thing more slowly;
//   - dying, so that a well-formed packet arriving afterwards gets no answer.
//
// Run longer with:
//
//	go test -run xxx -fuzz FuzzResponderPacketSequence -fuzztime 5m

// fuzzConn is a transport that can be fed packets and read back what the
// socket emitted. It does not depend on the cgo conformance harness, so it
// runs with CGO_ENABLED=0.
type fuzzConn struct {
	inbox  chan []byte
	peer   *fuzzPeer
	closed chan struct{}
	once   sync.Once

	mu      sync.Mutex
	emitted int
}

type fuzzPeer struct{}

func (p *fuzzPeer) Hash() string   { return "fuzz-peer" }
func (p *fuzzPeer) String() string { return "fuzz-peer" }

func newFuzzConn() *fuzzConn {
	return &fuzzConn{
		inbox:  make(chan []byte, 512),
		peer:   &fuzzPeer{},
		closed: make(chan struct{}),
	}
}

func (c *fuzzConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	select {
	case buf := <-c.inbox:
		return copy(b, buf), c.peer, nil
	case <-c.closed:
		return 0, nil, context.Canceled
	}
}

func (c *fuzzConn) WriteTo(b []byte, _ ConnectionPeer) (int, error) {
	c.mu.Lock()
	c.emitted++
	c.mu.Unlock()
	return len(b), nil
}

func (c *fuzzConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fuzzConn) inject(raw []byte) {
	cp := make([]byte, len(raw))
	copy(cp, raw)
	select {
	case c.inbox <- cp:
	case <-c.closed:
	default:
	}
}

func (c *fuzzConn) emittedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.emitted
}

func fuzzLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}

// splitPackets carves the fuzz input into a sequence of packets. The first
// byte of each record is its length, so the fuzzer controls how the stream is
// divided as well as what is in it.
func splitPackets(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 && len(out) < 32 {
		n := int(data[0])
		data = data[1:]
		if n > len(data) {
			n = len(data)
		}
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

const (
	fuzzConnID  = uint16(4242)
	fuzzSynSeq  = uint16(700)
	fuzzWindow  = uint32(1024 * 1024)
	fuzzSettle  = 15 * time.Millisecond
	fuzzTimeout = 20 * time.Second
)

func FuzzResponderPacketSequence(f *testing.F) {
	// Seeds: the conformance cases, plus a plain exchange, as
	// length-prefixed records.
	f.Add([]byte{0})
	f.Add(append([]byte{20}, make([]byte, 20)...))
	f.Add([]byte{4, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(append([]byte{22}, append(make([]byte, 20), 1, 0)...))

	data := newPacketBuilder(stData, fuzzConnID+1, 200000, fuzzWindow, fuzzSynSeq+1).
		WithAckNum(fuzzSynSeq).WithPayload([]byte("seed")).Build().Encode()
	f.Add(append([]byte{byte(len(data))}, data...))

	f.Fuzz(func(t *testing.T, input []byte) {
		packets := splitPackets(input)
		if len(packets) == 0 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), fuzzTimeout)
		defer cancel()

		conn := newFuzzConn()
		defer conn.Close()
		sock := NewSocket(ctx, conn, fuzzLogger())
		defer sock.Close()

		cid := NewConnectionID(conn.peer, fuzzConnID+1, fuzzConnID)
		accepted := make(chan struct{})
		go func() {
			defer close(accepted)
			_, _ = sock.AcceptWithCID(ctx, cid, NewConnectionConfig())
		}()

		// Establish, then feed the sequence.
		conn.inject(newPacketBuilder(stSyn, fuzzConnID, 100000, fuzzWindow, fuzzSynSeq).Build().Encode())
		time.Sleep(fuzzSettle)

		for _, pkt := range packets {
			conn.inject(pkt)
		}
		time.Sleep(fuzzSettle)

		// The socket must still be alive: after whatever the fuzzer sent, a
		// fresh connection still gets an answer.
		before := conn.emittedCount()
		fresh := NewConnectionID(conn.peer, fuzzConnID+3, fuzzConnID+2)
		freshDone := make(chan struct{})
		go func() {
			defer close(freshDone)
			freshCtx, freshCancel := context.WithTimeout(ctx, 2*time.Second)
			defer freshCancel()
			_, _ = sock.AcceptWithCID(freshCtx, fresh, NewConnectionConfig())
		}()
		time.Sleep(fuzzSettle)
		conn.inject(newPacketBuilder(stSyn, fuzzConnID+2, 300000, fuzzWindow, 900).Build().Encode())

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if conn.emittedCount() > before {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("the socket stopped answering after %d injected packets; "+
			"a peer that can do this can deny service to every connection on the port",
			len(packets))
	})
}
