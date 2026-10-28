package utp

import (
	"context"
	"testing"
	"time"
)

// tailBurstRecovery runs one sending connection on a virtual clock against a
// scripted peer with a fixed round trip, and loses every packet of one burst
// except its last: the pattern a queue overflow leaves at the end of a
// transfer, with too few packets after the holes for fast retransmission. It
// returns the virtual time from the burst leaving to the peer holding all of
// it, and how many packets the burst lost.
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
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()
	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, peerID+1, peerID)
	accepted := make(chan *Stream, 1)
	go func() {
		s, err := sock.AcceptWithCID(ctx, cid, cfg)
		if err != nil {
			t.Errorf("accept: %v", err)
		}
		accepted <- s
	}()
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(newPacketBuilder(stSyn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
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
		// A one-way delay of half the round trip: without a delay sample LEDBAT
		// holds the window where it is.
		b := newPacketBuilder(stState, peerID+1, uint32(clk.Now().UnixMicro()), 1<<20, peerSeq+1).
			WithAckNum(cum).WithTimestampDiffMicros(uint32(rtt.Microseconds() / 2))
		if stray != 0 && wrappingLessThan(cum+1, stray) {
			bits := make([]bool, 32)
			bits[stray-cum-2] = true
			b = b.WithSelectiveAck(newSelectiveAck(bits))
		}
		return b.Build().Encode()
	}
	sentData := func() []uint16 {
		var seqs []uint16
		for _, raw := range conn.takeEmitted() {
			if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stData {
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

	// The burst is out, all lost but its last. From here every packet
	// arrives and is acknowledged a round trip after it left; the clock
	// moves in small steps, so each timer fires when due.
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
// waiting for the retransmission timeout and then resending one packet per
// acknowledgement would. The loss probe resends the oldest packet after
// twice the smoothed round trip, and its acknowledgement starts recovery of
// the rest (probeAnswered). TestTailBurstLossAgainstLibutp compares with the
// reference implementation directly.
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
