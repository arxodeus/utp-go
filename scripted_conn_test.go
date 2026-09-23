package utp_go

// The scripted transport and helpers the conformance harness drives our side
// with. None of it needs libutp, so it builds without cgo, and so do the
// tests that use it on their own (zerowindow_test.go, dont_fragment_test.go).
// What compares against libutp is in conformance_harness_test.go, which does.

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

func conformanceLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// --- a scripted transport for our implementation ----------------------------

type scriptedPeer struct{ name string }

func (p *scriptedPeer) Hash() string { return p.name }

// scriptedConn is a Conn whose input is injected by the test and whose output
// is captured, so our socket can be driven exactly as the libutp driver is.
type scriptedConn struct {
	peer   *scriptedPeer
	closed chan struct{}
	once   sync.Once

	// inMu guards the queue of injected packets. notify carries a single
	// wake-up for a reader parked with nothing to read.
	inMu    sync.Mutex
	pending [][]byte
	holding bool
	notify  chan struct{}

	mu      sync.Mutex
	emitted [][]byte
}

func newScriptedConn() *scriptedConn {
	return &scriptedConn{
		peer:   &scriptedPeer{name: "conformance-peer"},
		closed: make(chan struct{}),
		notify: make(chan struct{}, 1),
	}
}

func (c *scriptedConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	for {
		c.inMu.Lock()
		if !c.holding && len(c.pending) > 0 {
			pkt := c.pending[0]
			c.pending = c.pending[1:]
			more := len(c.pending) > 0
			c.inMu.Unlock()
			if more {
				c.signal()
			}
			return copy(b, pkt), c.peer, nil
		}
		c.inMu.Unlock()
		select {
		case <-c.notify:
		case <-c.closed:
			return 0, nil, context.Canceled
		}
	}
}

func (c *scriptedConn) signal() {
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *scriptedConn) WriteTo(b []byte, _ ConnectionPeer) (int, error) {
	cp := make([]byte, len(b))
	copy(cp, b)
	c.mu.Lock()
	c.emitted = append(c.emitted, cp)
	c.mu.Unlock()
	return len(b), nil
}

func (c *scriptedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *scriptedConn) inject(pkt []byte) {
	c.inMu.Lock()
	c.pending = append(c.pending, pkt)
	holding := c.holding
	c.inMu.Unlock()
	if !holding {
		c.signal()
	}
}

// hold stops packets being delivered without stopping them being injected, so
// a test can queue a whole batch and then release it at once.
//
// Without it, a "batch" is only as batched as the injecting goroutine is fast:
// under -race, injecting eight packets in a loop took longer than the
// connection took to process one, so each arrived alone and was acknowledged
// alone. That measures the test, not the implementation. libutp has no such
// problem because its embedder hands it a whole batch of datagrams before
// calling utp_issue_deferred_acks(); hold/release gives our side the same
// starting position.
func (c *scriptedConn) hold() {
	c.inMu.Lock()
	c.holding = true
	c.inMu.Unlock()
}

func (c *scriptedConn) release() {
	c.inMu.Lock()
	c.holding = false
	c.inMu.Unlock()
	c.signal()
}

func (c *scriptedConn) takeEmitted() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.emitted
	c.emitted = nil
	return out
}

// pendingCount is how many injected packets our socket has not yet read.
//
// The settle loops below wait for a quiet period, and a quiet period that
// starts before our event loop has even been handed the packet measures
// nothing: under load the whole window can pass with the packet still sitting
// in this queue. Draining is the precondition for the wait to mean anything.
func (c *scriptedConn) pendingCount() int {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	return len(c.pending)
}

// awaitDelivered waits until our socket has read everything injected so far,
// or the limit passes. It reports whether the queue drained; a caller that
// injected nothing sees an immediate true.
func (c *scriptedConn) awaitDelivered(limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for c.pendingCount() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

func (c *scriptedConn) emittedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.emitted)
}

// settle waits until our implementation has stopped emitting.
//
// libutp is synchronous, so its side needs nothing like this. Ours is
// goroutine-driven with real timers, so the only honest way to know it has
// finished reacting is to watch until it goes quiet. That asymmetry is why
// the timing comparison in this corpus is coarse -- see CONFORMANCE.md.
func (c *scriptedConn) settle() {
	const quiet = 60 * time.Millisecond
	const limit = 3 * time.Second
	c.awaitDelivered(limit)
	deadline := time.Now().Add(limit)
	last := c.emittedCount()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		n := c.emittedCount()
		if n != last {
			last = n
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= quiet {
			return
		}
	}
}

// --- pinning our random source ----------------------------------------------

// pinRandom fixes the sequence numbers our implementation chooses, so they can
// be compared against libutp's scripted ones. It restores the real source on
// cleanup.
func pinRandom(v uint16) func() {
	prev := randomUint16Source
	randomUint16Source = func() uint16 { return v }
	return func() { randomUint16Source = prev }
}

func describePackets(pkts [][]byte) string {
	var b strings.Builder
	for i, raw := range pkts {
		p, err := DecodePacket(raw)
		if err != nil {
			fmt.Fprintf(&b, "\n  [%d] undecodable (%d bytes): %v", i, len(raw), err)
			continue
		}
		fmt.Fprintf(&b, "\n  [%d] %s conn=%d seq=%d ack=%d wnd=%d body=%d",
			i, p.Header.PacketType.String(), p.Header.ConnectionId,
			p.Header.SeqNum, p.Header.AckNum, p.Header.WndSize, len(p.Body))
		if p.Eack != nil {
			fmt.Fprintf(&b, " sack=%x", p.Eack.Encode())
		}
	}
	if b.Len() == 0 {
		return " (none)"
	}
	return b.String()
}

// loopbackAddr is the peer address our socket sees. The scripted transport
// never touches a socket, so it only has to be stable.
func loopbackAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 23456}
}
