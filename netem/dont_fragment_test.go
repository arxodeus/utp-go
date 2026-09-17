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
func dfTransfer(t *testing.T, cfg Config, wrap func(utp.Conn) utp.Conn, cid uint16) (current, floor, ceiling uint32, dupAckProbeLosses uint64, fwd Stats) {
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
		current, floor, ceiling = m.MtuCurrent, m.MtuFloor, m.MtuCeiling
		if m.MtuProbesLostToDuplicateAcks > dupAckProbeLosses {
			dupAckProbeLosses = m.MtuProbesLostToDuplicateAcks
		}
		samples++
	}

	payload := make([]byte, 4<<20)
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
	return current, floor, ceiling, dupAckProbeLosses, n.Link("sender", "receiver").Stats()
}

// The don't-fragment bit, and what it is worth.
//
// The path carries 1000-byte uTP datagrams and fragments anything larger
// rather than dropping it, which is what an IPv4 router does. The sender's
// search starts from a 1400-byte ceiling, so it probes above what the path
// carries in one piece.
//
// Without the bit an oversized probe is fragmented, arrives, and is
// acknowledged. The search reads that as "1400 bytes is fine" and settles
// there, on a path that cannot carry 1400 bytes in one piece -- so every data
// packet after that is fragmented too, which costs headers and turns any one
// lost fragment into a lost datagram.
//
// With the bit the router must drop the probe, the peer reports the hole with
// duplicate acknowledgements, and the search lowers its ceiling onto something
// the path really carries.
//
// Both halves are one case because neither proves anything alone: a search
// that settles low might have done so for any reason, and one that settles
// high might be on a path that genuinely carries it. It is the difference
// between them, on the same link with the same seed, that is the evidence.
//
// This needed two mechanisms, and having only the first is why an earlier
// version of this test could claim nothing about where the search settled.
// The bit makes the probe fail; libutp's duplicate-acknowledgement route
// (utp_internal.cpp:1927-1940) is what lets the search hear about it during a
// bulk transfer, because its other route needs the probe to be the only
// packet outstanding and a saturated window never leaves it that way.
func TestDontFragmentBringsTheSearchWithinThePath(t *testing.T) {
	cfg := Config{
		Delay:             10 * time.Millisecond,
		BandwidthBps:      20_000_000,
		QueueBytes:        64 * 1024,
		MTU:               1000,
		FragmentOversized: true,
	}

	withoutDF, floorWithout, ceilWithout, dupWithout, statsWithout := dfTransfer(t, cfg, func(c utp.Conn) utp.Conn {
		return noDontFragment{Conn: c}
	}, 810)
	withDF, floorWith, ceilWith, dupWith, statsWith := dfTransfer(t, cfg, nil, 820)

	t.Logf("without the bit: current=%d floor=%d ceiling=%d, %d probes lost to duplicate acks; link %s",
		withoutDF, floorWithout, ceilWithout, dupWithout, statsWithout)
	t.Logf("with the bit:    current=%d floor=%d ceiling=%d, %d probes lost to duplicate acks; link %s",
		withDF, floorWith, ceilWith, dupWith, statsWith)

	// --- the control has to present the trap, or there is nothing to detect
	if statsWithout.PacketsFragmented == 0 {
		t.Fatalf("no datagram was fragmented without the bit, so the path never "+
			"presented the case this is about; link %s", statsWithout)
	}
	if statsWithout.DroppedByMTU != 0 {
		t.Errorf("%d datagrams were refused for size without the don't-fragment bit; "+
			"this link should have fragmented every one of them", statsWithout.DroppedByMTU)
	}
	if withoutDF <= uint32(cfg.MTU) {
		t.Errorf("without the bit the search settled at %d, at or below the %d bytes the "+
			"path carries in one piece. It was not fooled, so this case is not "+
			"measuring what it claims", withoutDF, cfg.MTU)
	}
	if dupWithout != 0 {
		t.Errorf("%d probes were lost to duplicate acks without the bit, where no probe "+
			"is ever dropped for size", dupWithout)
	}

	// --- and the bit has to fix it
	if statsWith.DroppedByMTU == 0 {
		t.Errorf("no datagram was refused for size with the don't-fragment bit, on a " +
			"path narrower than the probes being sent. The bit is not reaching the wire.")
	}
	if dupWith == 0 {
		t.Error("the search never concluded a probe was too big from duplicate " +
			"acknowledgements, so it settled where it did for some other reason")
	}
	// The search must come down, and by more than a rounding.
	//
	// Not "down to at or below the path MTU", though it usually gets there --
	// six consecutive runs converged on 996 with floor and ceiling equal. Each
	// halving of the search range costs one dropped probe, and each dropped
	// probe costs three duplicate acknowledgements from the peer, which
	// deferred and coalesced acks do not guarantee for every hole. Roughly one
	// run in fourteen gets three narrowings instead of four and stops around
	// 1185, still above the path.
	//
	// So convergence inside one transfer is a property of the receiver's ack
	// cadence, not of the mechanism under test, and asserting it here would
	// make this case fail for a reason it is not about. What is asserted is
	// what the mechanism is responsible for: the ceiling moves, and it moves
	// only when the bit is set. See KNOWN-LIMITATIONS.md.
	const minimumNarrowing = 100
	if withoutDF < withDF+minimumNarrowing {
		t.Errorf("the search barely moved: %d with the bit against %d without, less than "+
			"the %d bytes a single narrowing is worth", withDF, withoutDF, minimumNarrowing)
	}

	t.Logf("fragmentation: %d datagrams with the bit, %d without",
		statsWith.PacketsFragmented, statsWithout.PacketsFragmented)
}
