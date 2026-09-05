//go:build cgo

package utp_go

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

// The M2 conformance harness.
//
// Two implementations are driven with identical packet sequences and every
// emitted packet is compared field by field. libutp is driven through the
// deterministic driver in native/libutp: virtual clock, scripted random
// source, packets in and out by explicit call. This side is driven through a
// scripted transport that does the same for our socket.
//
// Both sequence numbers and connection ids are pinned, so the comparison is
// byte-for-byte on everything except the fields listed in allowedToDiffer.

func conformanceLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// --- a scripted transport for our implementation ----------------------------

type scriptedPeer struct{ name string }

func (p *scriptedPeer) Hash() string { return p.name }

// scriptedConn is a Conn whose input is injected by the test and whose output
// is captured, so our socket can be driven exactly as the libutp driver is.
type scriptedConn struct {
	inbox  chan []byte
	peer   *scriptedPeer
	closed chan struct{}
	once   sync.Once

	mu      sync.Mutex
	emitted [][]byte
}

func newScriptedConn() *scriptedConn {
	return &scriptedConn{
		inbox:  make(chan []byte, 1024),
		peer:   &scriptedPeer{name: "conformance-peer"},
		closed: make(chan struct{}),
	}
}

func (c *scriptedConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	select {
	case buf := <-c.inbox:
		return copy(b, buf), c.peer, nil
	case <-c.closed:
		return 0, nil, context.Canceled
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

func (c *scriptedConn) inject(pkt []byte) { c.inbox <- pkt }

func (c *scriptedConn) takeEmitted() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.emitted
	c.emitted = nil
	return out
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

// --- comparison -------------------------------------------------------------

// fieldDiff is one field on which the two implementations disagreed.
type fieldDiff struct {
	Field  string
	Ours   any
	Libutp any
}

func (d fieldDiff) String() string {
	return fmt.Sprintf("%s: ours=%v libutp=%v", d.Field, d.Ours, d.Libutp)
}

// allowedToDiffer names the fields the corpus does not require to match, and
// why. Everything else must be identical.
//
// The brief's rule is that a deliberate divergence is asserted explicitly
// rather than hidden in a tolerance, so these are named here rather than
// quietly skipped, and the corpus reports when one actually differs.
var allowedToDiffer = map[string]string{
	"Timestamp": "wall-clock microseconds; libutp reads a virtual clock here " +
		"and our implementation reads the real one, so these cannot be made equal " +
		"without an injectable clock in our connection",
	"TimestampDiff": "derived from the peer's timestamp, so it inherits the above",
	"WndSize": "the advertised receive window is a local buffer-size choice, " +
		"not a protocol requirement; libutp advertises what its read buffer has " +
		"free and we advertise ours",
}

// comparePackets diffs two decoded packets field by field.
func comparePackets(ours, theirs *packet) []fieldDiff {
	var diffs []fieldDiff
	add := func(field string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			diffs = append(diffs, fieldDiff{Field: field, Ours: a, Libutp: b})
		}
	}

	add("PacketType", ours.Header.PacketType.String(), theirs.Header.PacketType.String())
	add("Version", ours.Header.Version, theirs.Header.Version)
	add("Extension", ours.Header.Extension, theirs.Header.Extension)
	add("ConnectionId", ours.Header.ConnectionId, theirs.Header.ConnectionId)
	add("SeqNum", ours.Header.SeqNum, theirs.Header.SeqNum)
	add("AckNum", ours.Header.AckNum, theirs.Header.AckNum)
	add("WndSize", ours.Header.WndSize, theirs.Header.WndSize)
	add("Timestamp", ours.Header.Timestamp, theirs.Header.Timestamp)
	add("TimestampDiff", ours.Header.TimestampDiff, theirs.Header.TimestampDiff)
	add("BodyLen", len(ours.Body), len(theirs.Body))
	add("Body", fmt.Sprintf("%x", ours.Body), fmt.Sprintf("%x", theirs.Body))

	oursAck, theirsAck := "none", "none"
	if ours.Eack != nil {
		oursAck = fmt.Sprintf("%x", ours.Eack.Encode())
	}
	if theirs.Eack != nil {
		theirsAck = fmt.Sprintf("%x", theirs.Eack.Encode())
	}
	add("SelectiveAck", oursAck, theirsAck)

	return diffs
}

// significant splits a diff list into the differences that matter and the ones
// allowedToDiffer explains.
func significant(diffs []fieldDiff) (bad, allowed []fieldDiff) {
	for _, d := range diffs {
		if _, ok := allowedToDiffer[d.Field]; ok {
			allowed = append(allowed, d)
		} else {
			bad = append(bad, d)
		}
	}
	return bad, allowed
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
