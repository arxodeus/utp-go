package utp_go

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// A socket must not answer every packet addressed to a connection it does not
// have. Doing so is both an incompatibility with libutp and an amplification
// vector: a peer that keeps sending to a torn-down connection would draw one
// RESET per packet.
//
// libutp remembers what it has already answered, keyed on
// (connection id, address, seq nr), for RST_INFO_TIMEOUT, and stops answering
// entirely past RST_INFO_LIMIT stored entries (utp_internal.cpp:2907-2945).

type testPeer struct{ name string }

func (p *testPeer) Hash() string { return p.name }

// recordingConn is a Conn that feeds injected packets to a socket and records
// what the socket writes back.
type recordingConn struct {
	inbox  chan []byte
	peer   *testPeer
	closed chan struct{}
	once   sync.Once

	mu   sync.Mutex
	sent []*packet
}

func newRecordingConn() *recordingConn {
	return &recordingConn{
		inbox:  make(chan []byte, 8192),
		peer:   &testPeer{name: "peer"},
		closed: make(chan struct{}),
	}
}

func (c *recordingConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	select {
	case buf := <-c.inbox:
		n := copy(b, buf)
		return n, c.peer, nil
	case <-c.closed:
		return 0, nil, context.Canceled
	}
}

func (c *recordingConn) WriteTo(b []byte, _ ConnectionPeer) (int, error) {
	pkt, err := DecodePacket(b)
	if err == nil {
		c.mu.Lock()
		c.sent = append(c.sent, pkt)
		c.mu.Unlock()
	}
	return len(b), nil
}

func (c *recordingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *recordingConn) inject(p *packet) { c.inbox <- p.Encode() }

func (c *recordingConn) countType(t PacketType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.sent {
		if p.Header.PacketType == t {
			n++
		}
	}
	return n
}

func testSocketLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// dataPacketFor builds an ST_DATA packet addressed to a connection id that no
// socket in the test has, which is what provokes a RESET.
func dataPacketFor(connID, seqNum uint16) *packet {
	return NewPacketBuilder(st_data, connID, uint32(time.Now().UnixMicro()), 100_000, seqNum).
		WithAckNum(1).
		WithPayload([]byte("x")).
		Build()
}

func TestResetIsSentOnceForARepeatedUnknownPacket(t *testing.T) {
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	// The same unanswerable packet, over and over. libutp answers the first
	// and stays quiet for the rest.
	const repeats = 50
	for i := 0; i < repeats; i++ {
		conn.inject(dataPacketFor(4242, 7))
	}

	waitFor(t, 3*time.Second, func() bool { return conn.countType(st_reset) >= 1 })
	time.Sleep(300 * time.Millisecond) // give any extras a chance to appear

	got := conn.countType(st_reset)
	t.Logf("%d identical unknown packets produced %d RESETs", repeats, got)
	if got != 1 {
		t.Errorf("got %d RESETs for %d repeats of one packet, want exactly 1", got, repeats)
	}
}

func TestResetIsSentForEachDistinctUnknownPacket(t *testing.T) {
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	// Distinct sequence numbers are distinct events, and each is answered --
	// suppression must not silence a genuinely new peer.
	const distinct = 20
	for i := 0; i < distinct; i++ {
		conn.inject(dataPacketFor(4242, uint16(i)))
	}

	waitFor(t, 3*time.Second, func() bool { return conn.countType(st_reset) >= distinct })
	got := conn.countType(st_reset)
	t.Logf("%d distinct unknown packets produced %d RESETs", distinct, got)
	if got != distinct {
		t.Errorf("got %d RESETs for %d distinct packets, want %d", got, distinct, distinct)
	}
}

func TestResetIsNeverSentInReplyToAReset(t *testing.T) {
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	// Answering a reset with a reset lets two hosts trade them forever.
	// libutp handles ST_RESET in its own branch and returns without
	// responding (utp_internal.cpp:2850-2881).
	for i := 0; i < 20; i++ {
		conn.inject(NewPacketBuilder(st_reset, 4242, uint32(time.Now().UnixMicro()), 100_000, uint16(i)).Build())
	}

	time.Sleep(500 * time.Millisecond)
	if got := conn.countType(st_reset); got != 0 {
		t.Errorf("replied to unknown RESETs with %d RESETs, want 0", got)
	}
}

// Past its stored-entry limit, a socket stops answering unknown packets
// entirely rather than letting the table grow without bound.
func TestResetStopsAtTheStoredLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("injects more than rstInfoLimit packets")
	}
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	total := rstInfoLimit + 500
	for i := 0; i < total; i++ {
		// Distinct connection ids so every one is a new entry.
		conn.inject(dataPacketFor(uint16(i), uint16(i)))
	}

	waitFor(t, 10*time.Second, func() bool { return conn.countType(st_reset) > rstInfoLimit-10 })
	time.Sleep(500 * time.Millisecond)

	got := conn.countType(st_reset)
	t.Logf("%d distinct unknown packets produced %d RESETs (limit %d)", total, got, rstInfoLimit)
	if got > rstInfoLimit+1 {
		t.Errorf("sent %d RESETs, more than the %d limit allows", got, rstInfoLimit)
	}
	if got < rstInfoLimit/2 {
		t.Errorf("sent only %d RESETs; suppression is far too aggressive", got)
	}
}

func waitFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
