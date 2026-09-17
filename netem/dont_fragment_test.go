package netem

import (
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// noDontFragment hides an Endpoint's WriteToDontFragment.
//
// Embedding the utp.Conn *interface* rather than the concrete endpoint is what
// does it: only ReadFrom, WriteTo and Close are promoted, so a type assertion
// to utp.DontFragmentWriter fails. This is every Conn written before that
// interface existed, and it is the control these cases are measured against.
type noDontFragment struct{ utp.Conn }

// dfTransfer runs one transfer and reports where the sender's MTU search
// settled, plus the link's stats. wrap is applied to the sender's Conn.
func dfTransfer(t *testing.T, cfg Config, wrap func(utp.Conn) utp.Conn, cid uint16) (current uint32, fwd Stats) {
	t.Helper()

	n := NewNetwork(51)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	var senderConn utp.Conn = a
	if wrap != nil {
		senderConn = wrap(a)
	}
	sendSock := utp.WithSocket(ctx, senderConn, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(a.Addr(), cid+1, cid)
	connectCid := utp.NewConnectionId(b.Addr(), cid, cid+1)

	var (
		mu      sync.Mutex
		samples int
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		current = m.MtuCurrent
		samples++
	}

	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		if _, err := stream.ReadToEOF(ctx, &buf); err != nil {
			t.Errorf("read: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			t.Errorf("connect: %v", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if samples == 0 {
		t.Fatal("no metric samples recorded; the test observed nothing")
	}
	return current, n.Link("sender", "receiver").Stats()
}

// The don't-fragment bit reaches the wire, and the router acts on it.
//
// The path carries 1000-byte uTP datagrams and fragments anything larger
// rather than dropping it, which is what an IPv4 router does. The sender's
// search starts from a 1400-byte ceiling, so it probes above what the path
// carries in one piece.
//
// Without the bit every oversized probe is fragmented and delivered, so the
// path's narrowness is invisible: nothing is ever refused for size. With the
// bit the router has no choice but to drop the probe. That difference, on the
// same link with the same seed, is what this measures.
//
// What it deliberately does NOT claim is that the search then settles lower.
// It does not, and the reason is a separate missing mechanism rather than
// anything about this bit. libutp lowers its ceiling from a lost probe in two
// places: a retransmission timeout with the probe as the only packet
// outstanding (utp_internal.cpp:1152-1167), and a third duplicate
// acknowledgement pointing at the packet before the probe (:1927-1940). Only
// the first is implemented here, and it cannot fire during a bulk transfer,
// because a saturated window always has more than one packet outstanding. So
// the probe is dropped, its retransmission goes out fragmentable as libutp
// intends, arrives, and is acknowledged -- and the search concludes the size
// was fine.
//
// Recorded in KNOWN-LIMITATIONS.md. Until it is closed the bit is necessary
// but not sufficient, and a test claiming the search learns from it would be
// claiming something false.
func TestDontFragmentReachesTheWire(t *testing.T) {
	cfg := Config{
		Delay:             10 * time.Millisecond,
		BandwidthBps:      20_000_000,
		QueueBytes:        64 * 1024,
		MTU:               1000,
		FragmentOversized: true,
	}

	withoutDF, statsWithout := dfTransfer(t, cfg, func(c utp.Conn) utp.Conn {
		return noDontFragment{Conn: c}
	}, 810)
	withDF, statsWith := dfTransfer(t, cfg, nil, 820)

	t.Logf("without the bit: settled at %d; link %s", withoutDF, statsWithout)
	t.Logf("with the bit:    settled at %d; link %s", withDF, statsWith)

	// The control has to present the trap, or there is nothing to detect.
	if statsWithout.PacketsFragmented == 0 {
		t.Fatalf("no datagram was fragmented without the bit, so the path never "+
			"presented the case this is about; link %s", statsWithout)
	}
	if statsWithout.DroppedByMTU != 0 {
		t.Errorf("%d datagrams were refused for size without the don't-fragment bit; "+
			"this link should have fragmented every one of them", statsWithout.DroppedByMTU)
	}

	// And the bit has to change what the router does.
	if statsWith.DroppedByMTU == 0 {
		t.Errorf("no datagram was refused for size with the don't-fragment bit, on a " +
			"path narrower than the probes being sent. The bit is not reaching the wire.")
	}

	// Both transfers still complete -- the bit must not cost delivery. A
	// dropped probe is retransmitted fragmentable, which is exactly what
	// libutp relies on (utp_internal.cpp:898-905).
	if statsWith.PacketsDelivered == 0 || statsWithout.PacketsDelivered == 0 {
		t.Error("a transfer delivered nothing")
	}
}
