package utp

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A loss probe that fired because the round trip jumped, not because
// anything was lost, resends the probe and nothing else.
//
// A queue filling in slow start can raise the round trip faster than the
// smoothed estimate the probe's timer uses. The probe then resends the oldest
// packet while it is still queued, and the first acknowledgement after the
// probe is the original's, which must not be taken as evidence that earlier
// packets were lost. Here the scripted peer reports its delay as a real one
// does, so its acknowledgements identify the packet that drew them, and one
// round's acknowledgements are held for four round trips.
func TestSpuriousLossProbeResendsOnlyItself(t *testing.T) {
	t.Run("queue", func(t *testing.T) { spuriousProbe(t, false) })
	// The same with a peer that reports no delay, and the acknowledgements
	// held until the probe has gone and then released together, as when this
	// process stalls: the original's arrives 5 ms after the probe, under the
	// path's 50 ms minimum round trip, which is RFC 8985's test.
	t.Run("stall, no delay reported", func(t *testing.T) { spuriousProbe(t, true) })
}

func spuriousProbe(t *testing.T, stall bool) {
	const (
		ourSeq  = 0x4321
		peerID  = 6300
		peerSeq = 900
		rtt     = 50 * time.Millisecond
		spike   = 4 * rtt
		burst   = 20
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
		conn.inject(newPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
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

	// The peer: what it holds, and the send timestamp of the last packet it
	// received. Its acknowledgement carries its own clock, half a round
	// trip before the acknowledgement arrives, and the delay it measured,
	// so their difference is that packet's send timestamp, as with a real
	// peer.
	got := map[uint16]bool{}
	cum := uint16(ourSeq - 1)
	// In the stalled case the peer stops reporting a delay only for the
	// spiked round: LEDBAT holds the window without delay samples, so it
	// could not grow first otherwise.
	noDelay := false
	ack := func(lastStamp uint32) []byte {
		for got[cum+1] {
			cum++
		}
		peerNow := uint32(clk.Now().Add(-rtt / 2).UnixMicro())
		diff := peerNow - lastStamp
		if noDelay {
			diff = 0
		}
		return newPacketBuilder(st_state, peerID+1, peerNow, 1<<20, peerSeq+1).
			WithAckNum(cum).WithTsDiffMicros(diff).Build().Encode()
	}
	type sent struct {
		seq   uint16
		stamp uint32
		at    time.Time
	}
	everSent := map[uint16]bool{}
	resent := 0
	drain := func() []sent {
		var out []sent
		for _, raw := range conn.takeEmitted() {
			p, err := decodePacket(raw, false)
			if err != nil || p.Header.PacketType != st_data {
				continue
			}
			if everSent[p.Header.SeqNum] {
				resent++
			}
			everSent[p.Header.SeqNum] = true
			out = append(out, sent{p.Header.SeqNum, uint32(p.Header.Timestamp), clk.Now()})
		}
		return out
	}

	// Grow the window, every packet acknowledged one round trip after it left.
	var spikeRound []sent
	for round := 0; ; round++ {
		if round > 200 {
			t.Fatalf("the window never reached %d packets", burst)
		}
		clk.AwaitQuiet()
		pkts := drain()
		if len(pkts) >= burst {
			spikeRound = pkts
			break
		}
		clk.Advance(rtt)
		for _, p := range pkts {
			got[p.seq] = true
		}
		if len(pkts) > 0 {
			last := pkts[len(pkts)-1].stamp
			clk.AwaitReactionTo(func() { conn.inject(ack(last)) })
		}
	}
	resentBefore := resent
	noDelay = stall

	// This round's packets all arrive and each is acknowledged on arrival,
	// but a queue holds them for four round trips. Whatever is sent
	// meanwhile, the probe included, comes after them.
	const step = 5 * time.Millisecond
	pending := append([]sent(nil), spikeRound...)
	var probes []sent
	start := clk.Now()
	lastOfRound := spikeRound[len(spikeRound)-1].seq
	var settled time.Time
	for settled.IsZero() || clk.Now().Sub(settled) < 2*rtt {
		if settled.IsZero() && !wrappingLessThan(cum, lastOfRound) {
			settled = clk.Now()
		}
		if clk.Now().Sub(start) > 10*spike {
			t.Fatalf("the spiked round was never acknowledged; peer holds through %d", cum)
		}
		clk.Advance(step)
		keep := pending[:0]
		for _, p := range pending {
			due := clk.Now().Sub(p.at) >= spike
			if stall {
				due = len(probes) > 0 && clk.Now().Sub(probes[0].at) >= step
			}
			if due {
				got[p.seq] = true
				p := p
				clk.AwaitReactionTo(func() { conn.inject(ack(p.stamp)) })
			} else {
				keep = append(keep, p)
			}
		}
		pending = keep
		clk.AwaitQuiet()
		for _, p := range drain() {
			if p.seq-spikeRound[0].seq < uint16(len(spikeRound)) {
				probes = append(probes, p)
			}
			pending = append(pending, sent{p.seq, p.stamp, clk.Now()})
		}
	}
	extra := resent - resentBefore
	how := fmt.Sprintf("held %v by a queue", spike)
	if stall {
		how = "held until the probe had gone, no delay reported"
	}
	t.Logf("a %d-packet round %s: %d resent (%d of them packets of that round)",
		len(spikeRound), how, extra, len(probes))
	if extra > 1 {
		t.Fatalf("%d packets resent though nothing was lost; want at most the probe", extra)
	}
}
