package utp_go

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// A retransmission timeout resends one packet, the oldest, and the rest follow
// as acknowledgements come back.
//
// libutp marks every packet in flight need_resend and resends only the oldest
// (utp_internal.cpp:1230-1252). The rest go out from flush_packets as the
// window allows, or one per acknowledgement through the fast-timeout path
// (:2256-2282). This library used to resend every packet whose own timer
// fired, which on a blacked-out link was the whole window at the timeout and
// most of it again a second later (netem.TestRTOBurstMeasure).
func TestRetransmissionTimeoutResendsOnlyTheOldest(t *testing.T) {
	t.Run("ack opens the window", func(t *testing.T) { rtoResendCase(t, 1<<20, false) })
	// An acknowledgement advertising less than a packet of room leaves
	// processWrites unable to send anything, so only the fast-timeout retry
	// can bring the next packet back -- and libutp's does, because it calls
	// send_packet directly rather than through flush_packets and is_full
	// (utp_internal.cpp:2280-2281). Without this case the fast timeout could
	// be missing and the other case would still pass.
	t.Run("ack leaves less than a packet of room", func(t *testing.T) { rtoResendCase(t, 100, true) })
}

func rtoResendCase(t *testing.T, ackWindow uint32, belowOnePacket bool) {
	const (
		ourSeq  = 0x4321
		peerID  = 6000
		peerSeq = 900
		step    = 10 * time.Millisecond
	)

	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(ourSeq)()

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }
	// The opening congestion window is two maximum-size packets and the
	// first packets are the MTU search's midpoint, well under the maximum,
	// so a larger maximum puts three in flight. Three is what it takes for
	// the resend loop in processWrites to show: the fast-timeout retry
	// brings back the second, and only that loop the third.
	cfg.MaxPacketSize = 4000

	cid := NewConnectionId(conn.peer, peerID+1, peerID)
	accepted := make(chan *UtpStream, 1)
	go func() {
		stream, err := sock.AcceptWithCid(ctx, cid, cfg)
		if err == nil {
			accepted <- stream
		}
	}()

	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	var stream *UtpStream
	select {
	case stream = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the accept did not complete within 10s of the SYN")
	}
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	// Complete the handshake as a peer would, with data that acknowledges our
	// SYN-ACK.
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 150000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("hi")).Build().Encode())
	})
	conn.takeEmitted()

	// Write more than the opening window holds, so there are packets in
	// flight and more waiting behind them.
	// The writer is not a participant in the virtual clock, so wait for its
	// packets in real time rather than by advancing the clock.
	go func() { _, _ = stream.Write(ctx, bytes.Repeat([]byte("x"), 8000)) }()
	waitFor(t, 5*time.Second, func() bool { return conn.emittedCount() > 0 })
	clk.AwaitQuiet()
	first := dataPackets(t, conn.takeEmitted())
	if len(first) < 3 {
		t.Fatalf("%d data packets in flight; this case needs at least three, or "+
			"the resend loop in processWrites is never exercised", len(first))
	}
	oldest := seqOf(t, first[0])
	t.Logf("%d packets in flight, oldest %d", len(first), oldest)

	// Say nothing, and wait for the timeout.
	var atTimeout [][]byte
	var waited time.Duration
	for waited < 30*time.Second && len(atTimeout) == 0 {
		clk.Advance(step)
		waited += step
		atTimeout = dataPackets(t, conn.takeEmitted())
	}
	if len(atTimeout) == 0 {
		t.Fatalf("nothing resent within %v", waited)
	}
	if len(atTimeout) != 1 || seqOf(t, atTimeout[0]) != oldest {
		t.Fatalf("at the timeout (%v) sent %s; libutp resends one packet, the "+
			"oldest (%d) -- utp_internal.cpp:1249-1251", waited,
			describePackets(atTimeout), oldest)
	}

	// Nothing else leaves before the next deadline: the others wait for an
	// acknowledgement or for the next timeout.
	for i := 0; i < 50; i++ {
		clk.Advance(step)
		if more := dataPackets(t, conn.takeEmitted()); len(more) != 0 {
			t.Fatalf("%v after the timeout, with nothing acknowledged, sent %s",
				time.Duration(i+1)*step, describePackets(more))
		}
	}

	// The acknowledgement for the oldest brings the next oldest back first,
	// ahead of anything new: fast-timeout retry, then flush_packets in order.
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_state, peerID+1, 250000, ackWindow, peerSeq+2).
			WithAckNum(oldest).Build().Encode())
	})
	clk.Advance(step)
	after := dataPackets(t, conn.takeEmitted())
	if len(after) == 0 {
		t.Fatal("the acknowledgement for the resent packet drew nothing")
	}
	if got := seqOf(t, after[0]); got != oldest+1 {
		t.Fatalf("after the acknowledgement for %d, sent %s first; the next "+
			"oldest (%d) goes before anything else", oldest, describePackets(after), oldest+1)
	}
	if belowOnePacket && len(after) != 1 {
		t.Fatalf("with %d bytes of peer window, sent %s; only the fast-timeout "+
			"retry should have gone", ackWindow, describePackets(after))
	}
	for i := 1; i < len(after); i++ {
		if seqOf(t, after[i]) != seqOf(t, after[i-1])+1 {
			t.Fatalf("after the acknowledgement, sent out of order: %s", describePackets(after))
		}
	}
	if !belowOnePacket {
		// Everything given up at the timeout goes out before any new data:
		// libutp's flush_packets walks its buffer oldest first.
		if len(after) < len(first)-1 || seqOf(t, after[len(first)-2]) != oldest+uint16(len(first)-1) {
			t.Fatalf("after the acknowledgement, sent %s; the %d packets still "+
				"owed (%d..%d) go first", describePackets(after), len(first)-1,
				oldest+1, oldest+uint16(len(first)-1))
		}
	}
}

func dataPackets(t *testing.T, emitted [][]byte) [][]byte {
	t.Helper()
	var out [][]byte
	for _, b := range emitted {
		pkt, err := DecodePacket(b)
		if err != nil {
			t.Fatalf("emitted packet does not decode: %v", err)
		}
		if pkt.Header.PacketType == st_data {
			out = append(out, b)
		}
	}
	return out
}

func seqOf(t *testing.T, b []byte) uint16 {
	t.Helper()
	pkt, err := DecodePacket(b)
	if err != nil {
		t.Fatalf("packet does not decode: %v", err)
	}
	return pkt.Header.SeqNum
}
