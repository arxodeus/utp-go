package utp_go

import (
	"context"
	"testing"
	"time"
)

// tailBurstRecovery runs one connection, ours sending, on a virtual clock
// against a scripted peer with a fixed round trip, and loses every packet of
// one burst but its last -- the shape a queue overflow leaves at the end of a
// transfer: too few packets after the holes for a selective ack to trigger
// fast retransmission. It returns the virtual time from the burst leaving to
// the peer holding all of it, and how many packets the burst lost.
func tailBurstRecovery(t *testing.T, rtt time.Duration, burst int) (time.Duration, int) {
	t.Helper()
	const (
		ourSeq  = 0x4321
		peerID  = 6300
		peerSeq = 900
	)
	clk := newVirtualClock(time.Unix(0, 0).Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(ourSeq)()

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()
	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

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
	conn.takeEmitted()

	clk.AwaitReactionTo(func() {
		go func() { _, _ = stream.Write(ctx, make([]byte, 2<<20)) }()
	})

	// The peer: what it holds, its cumulative acknowledgement, and the one
	// packet past the burst it was given.
	got := map[uint16]bool{}
	cum := uint16(ourSeq - 1)
	var stray uint16
	ack := func() []byte {
		for got[cum+1] {
			cum++
		}
		// A one-way delay of half the round trip: without a delay sample
		// LEDBAT leaves the window where it is, in libutp as here.
		b := NewPacketBuilder(st_state, peerID+1, uint32(clk.Now().UnixMicro()), 1<<20, peerSeq+1).
			WithAckNum(cum).WithTsDiffMicros(uint32(rtt.Microseconds() / 2))
		if stray != 0 && wrappingLessThan(cum+1, stray) {
			bits := make([]bool, 32)
			bits[stray-cum-2] = true
			b = b.WithSelectiveAck(NewSelectiveAck(bits))
		}
		return b.Build().Encode()
	}
	sentData := func() []uint16 {
		var seqs []uint16
		for _, raw := range conn.takeEmitted() {
			if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_data {
				seqs = append(seqs, p.Header.SeqNum)
			}
		}
		return seqs
	}

	// Grow the window: every packet arrives, one round trip later.
	var lostFrom, lostTo uint16
	for round := 0; ; round++ {
		if round > 200 {
			t.Fatalf("the window never reached %d packets", burst)
		}
		clk.AwaitQuiet()
		seqs := sentData()
		if len(seqs) >= burst {
			lostFrom, lostTo, stray = seqs[0], seqs[len(seqs)-2], seqs[len(seqs)-1]
			got[stray] = true
			break
		}
		for _, s := range seqs {
			got[s] = true
		}
		clk.Advance(rtt)
		clk.AwaitReactionTo(func() { conn.inject(ack()) })
	}

	// The burst is out, all lost but its last. From here every packet sent
	// arrives, and is acknowledged a round trip after it left; between
	// packets the clock moves in small steps, so a timer fires when it is due.
	start := clk.Now()
	const step = 5 * time.Millisecond
	var pending []struct {
		seq uint16
		at  time.Time
	}
	for wrappingLessThan(cum, lostTo) {
		if clk.Now().Sub(start) > time.Minute {
			t.Fatalf("burst %d-%d not recovered after a minute; peer holds through %d", lostFrom, lostTo, cum)
		}
		clk.AwaitQuiet()
		for _, s := range sentData() {
			pending = append(pending, struct {
				seq uint16
				at  time.Time
			}{s, clk.Now().Add(rtt)})
		}
		clk.Advance(step)
		var due []uint16
		keep := pending[:0]
		for _, p := range pending {
			if !clk.Now().Before(p.at) {
				due = append(due, p.seq)
			} else {
				keep = append(keep, p)
			}
		}
		pending = keep
		if len(due) > 0 {
			for _, s := range due {
				got[s] = true
			}
			clk.AwaitReactionTo(func() { conn.inject(ack()) })
		}
	}
	return clk.Now().Sub(start), int(lostTo-lostFrom) + 1
}

// A burst lost at the tail of what is in flight is recovered no slower than
// libutp would recover it. The bound here is libutp's rule worked from its
// source; TestTailBurstLossAgainstLibutp measures libutp itself in the same
// scenario: 1.75 s for 24 packets, against 1.3 s here, and 5.3 s here before
// the probe's acknowledgement started recovery.
//
// libutp has no loss probe. With too few packets past the holes for fast
// retransmission, it waits for its retransmission timeout -- at least a
// second (utp_internal.cpp:1380) -- which marks everything outstanding lost,
// and then resends one packet per acknowledgement, the fast-timeout retry
// (:2256-2282): the timeout plus about a round trip per hole.
//
// This library's loss probe resends the oldest packet first, after twice the
// smoothed round trip. Its acknowledgement retired that packet, which
// restarted the timeout, and re-armed the probe -- so the timeout never fired,
// and the burst came back one packet per probe timeout. In the 1000-transfer
// stress test, with round-trip estimates inflated by load, that was one packet
// every 4.3 seconds for 13 holes, and transfers took minutes
// (TestManyConcurrentTransfers past its budget about one run in four).
func TestTailBurstLossRecoversNoSlowerThanLibutp(t *testing.T) {
	const rtt = 50 * time.Millisecond
	took, lost := tailBurstRecovery(t, rtt, 25)
	libutp := time.Second + time.Duration(lost+1)*rtt
	t.Logf("%d packets lost at the tail: recovered in %v; libutp's timeout and retry would take about %v",
		lost, took, libutp)
	if took > libutp {
		t.Fatalf("recovered %d lost packets in %v, slower than libutp's %v", lost, took, libutp)
	}
}
