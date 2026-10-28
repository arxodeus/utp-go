package utp

// The scripted transport and helpers the conformance harness drives our side
// with. None of it needs cgo, so tests that use it alone (zerowindow_test.go,
// dont_fragment_test.go) build without it. The comparison against the
// reference implementation is in conformance_harness_test.go.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func conformanceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}

// --- a scripted transport for our implementation ----------------------------

type scriptedPeer struct{ name string }

func (p *scriptedPeer) Hash() string { return p.name }

// scriptedConn is a Conn whose input is injected by the test and whose output
// is captured, so our socket can be driven as the reference driver is.
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

// Queued implements QueuedReader: injected packets not held back are what
// ReadFrom returns at once, as a real socket's queue is.
func (c *scriptedConn) Queued() int {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	if c.holding {
		return 0
	}
	return len(c.pending)
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

func (c *scriptedConn) takeEmitted() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.emitted
	c.emitted = nil
	return out
}

// pendingCount returns how many injected packets our socket has not yet read.
// The settle loops wait for a quiet period, which means nothing if it starts
// before the packet has even been read.
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

// settle waits until our implementation has stopped emitting. The reference
// driver is synchronous; ours runs goroutines and real timers, so the only
// way to know it has finished reacting is to watch until it goes quiet. This
// is why timing comparisons here are coarse.
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

// pinRandom fixes the sequence numbers our implementation chooses, so they
// can be compared with the reference driver's scripted ones. It restores the
// real source on cleanup.
func pinRandom(v uint16) func() {
	prev := randomUint16Source.Load()
	pinned := func() uint16 { return v }
	randomUint16Source.Store(&pinned)
	return func() { randomUint16Source.Store(prev) }
}

func describePackets(pkts [][]byte) string {
	var b strings.Builder
	for i, raw := range pkts {
		p, err := decodePacket(raw, false)
		if err != nil {
			fmt.Fprintf(&b, "\n  [%d] undecodable (%d bytes): %v", i, len(raw), err)
			continue
		}
		fmt.Fprintf(&b, "\n  [%d] %s conn=%d seq=%d ack=%d wnd=%d body=%d",
			i, p.Header.PacketType.String(), p.Header.ConnectionID,
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

// Pinning the random source while something draws from it is safe: a
// socket's reader can still be answering a datagram with a reset after Close
// has returned. Meaningful under the race detector.
func TestRandomSourcePinnedWhileInUse(t *testing.T) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	var drawing atomic.Bool
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
				randomUint16()
				drawing.Store(true)
			}
		}
	}()
	for !drawing.Load() {
		runtime.Gosched()
	}
	for start := time.Now(); time.Since(start) < 20*time.Millisecond; {
		restore := pinRandom(1)
		restore()
	}
	close(done)
	<-stopped
}
