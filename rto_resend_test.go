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

	vc := newVirtualAccepted(t, 4000)
	clk, conn, stream, ctx := vc.clk, vc.conn, vc.stream, vc.ctx
	_ = step

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

// virtualAccepted is a connection we accepted, on the virtual clock, with
// the handshake completed by one data packet from the peer.
type virtualAccepted struct {
	clk    *virtualClock
	conn   *scriptedConn
	stream *UtpStream
	ctx    context.Context
}

// newVirtualAccepted accepts a connection with our sequence number pinned to
// 0x4321, the peer's id 6000 and its first sequence number 900. maxPacket, if
// non-zero, sets MaxPacketSize.
func newVirtualAccepted(t *testing.T, maxPacket uint16) *virtualAccepted {
	t.Helper()
	const (
		ourSeq  = 0x4321
		peerID  = 6000
		peerSeq = 900
	)
	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(pinRandom(ourSeq))

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	t.Cleanup(sock.Close)

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }
	if maxPacket != 0 {
		cfg.MaxPacketSize = maxPacket
	}

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
	return &virtualAccepted{clk: clk, conn: conn, stream: stream, ctx: ctx}
}

// writeOne writes b and returns the data packets it drew.
func (v *virtualAccepted) writeOne(t *testing.T, b []byte) [][]byte {
	t.Helper()
	go func() { _, _ = v.stream.Write(v.ctx, b) }()
	waitFor(t, 5*time.Second, func() bool { return v.conn.emittedCount() > 0 })
	v.clk.AwaitQuiet()
	return dataPackets(t, v.conn.takeEmitted())
}

// An acknowledgement that retires everything in flight restarts the
// retransmission deadline, so the next packet's timeout runs from its own
// send.
//
// It did not. The sent-packet bookkeeping reported "nothing left
// unacknowledged" as an error, and processAck returned before it disarmed the
// retired packets' timers or restarted the deadline. On any connection not
// sending flat out -- request and response, a trickle of protocol messages --
// that was every acknowledgement. The next packet then found timers still
// armed, so it did not start a deadline of its own, and was resent against
// whatever deadline the earlier packet had left: here 3 seconds from the first
// send, where libutp resends one timeout after the second (its ack_packet
// restarts rto_timeout, utp_internal.cpp:1388-1389).
func TestAckRetiringEverythingRestartsTheDeadline(t *testing.T) {
	const (
		ourSeq  = 0x4321
		peerID  = 6000
		peerSeq = 900
	)
	vc := newVirtualAccepted(t, 0)

	first := vc.writeOne(t, []byte("first"))
	if len(first) != 1 {
		t.Fatalf("first write sent %d packets", len(first))
	}
	vc.clk.Advance(100 * time.Millisecond)
	vc.clk.AwaitReactionTo(func() {
		vc.conn.inject(NewPacketBuilder(st_state, peerID+1, 250000, 1<<20, peerSeq+2).
			WithAckNum(seqOf(t, first[0])).Build().Encode())
	})
	vc.conn.takeEmitted()

	vc.clk.Advance(100 * time.Millisecond)
	second := vc.writeOne(t, []byte("second"))
	if len(second) != 1 {
		t.Fatalf("second write sent %d packets", len(second))
	}

	// One RTT sample of 100ms puts the timeout at libutp's 1000ms floor.
	var waited time.Duration
	for waited < 5*time.Second {
		vc.clk.Advance(5 * time.Millisecond)
		waited += 5 * time.Millisecond
		if again := dataPackets(t, vc.conn.takeEmitted()); len(again) > 0 {
			break
		}
	}
	if waited > time.Second+defaultRetransmitTickInterval {
		t.Errorf("the second packet was resent %v after it was sent; with a 1s timeout "+
			"and the first packet acknowledged, it is owed a resend within one wheel tick of 1s",
			waited)
	}
}
