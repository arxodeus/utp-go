package utp

import (
	"context"
	"testing"
	"time"
)

// When the peer's stream ends, the reference implementation arms its
// retransmission timeout 60 ms out, and if anything is in flight it is then
// treated as a timeout: everything marked for resend, the window cut to one
// packet, and the oldest resent. A peer that half-closes while the other side
// is still sending, as with a request followed by a response, costs the
// sender its window.
//
// This library does not do that. Both halves are checked: the reference
// implementation's resend moves forward to its next timeout pass after the
// FIN, and ours stays where it was.

// libutpFirstResendAfterResponse returns how long after sending its response
// the reference implementation first resends it, with or without the peer's
// FIN arriving.
func libutpFirstResendAfterResponse(t *testing.T, peerFin bool) time.Duration {
	t.Helper()
	drv, err := newRefDriver(t, 1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.PushRandom(corpusPinnedSeq)
	drv.Listen()
	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.Inject(newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("request")).Build().Encode())
	drv.IssueAcks()
	drv.ClearEmitted()
	if _, err := drv.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	drv.IssueAcks()
	drv.ClearEmitted()
	if peerFin {
		drv.Inject(newPacketBuilder(stFin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
			WithAckNum(corpusPinnedSeq - 1).Build().Encode())
		drv.IssueAcks()
		drv.ClearEmitted()
	}
	for ms := 100; ms <= 8000; ms += 100 {
		drv.Advance(100_000)
		drv.CheckTimeouts()
		drv.IssueAcks()
		for _, raw := range drv.Emitted() {
			if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stData {
				return time.Duration(ms) * time.Millisecond
			}
		}
		drv.ClearEmitted()
	}
	t.Fatal("libutp never resent its response")
	return 0
}

// ourFirstResendAfterResponse is the same for this library, on a virtual
// clock.
func ourFirstResendAfterResponse(t *testing.T, peerFin bool) time.Duration {
	t.Helper()
	const (
		ourSeq  = 0x4321
		peerID  = 6200
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
	clk.AwaitReactionTo(func() {
		conn.inject(newPacketBuilder(stData, peerID+1, 190000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("request")).Build().Encode())
	})
	conn.takeEmitted()

	written := make(chan error, 1)
	clk.AwaitReactionTo(func() {
		go func() {
			_, err := stream.Write(ctx, []byte("response"))
			written <- err
		}()
	})
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	clk.AwaitQuiet()
	var sentSeq uint16
	for _, raw := range conn.takeEmitted() {
		if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stData {
			sentSeq = p.Header.SeqNum
		}
	}
	if sentSeq == 0 {
		t.Fatal("the response was not sent")
	}
	if peerFin {
		clk.AwaitReactionTo(func() {
			conn.inject(newPacketBuilder(stFin, peerID+1, 200000, 1<<20, peerSeq+2).
				WithAckNum(ourSeq - 1).Build().Encode())
		})
		conn.takeEmitted()
	}
	for ms := 100; ms <= 8000; ms += 100 {
		clk.Advance(100 * time.Millisecond)
		for _, raw := range conn.takeEmitted() {
			if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stData && p.Header.SeqNum == sentSeq {
				return time.Duration(ms) * time.Millisecond
			}
		}
	}
	t.Fatal("the response was never resent")
	return 0
}

func TestPeerFinDoesNotTimeOutWhatIsInFlight(t *testing.T) {
	libPlain, libFin := libutpFirstResendAfterResponse(t, false), libutpFirstResendAfterResponse(t, true)
	ourPlain, ourFin := ourFirstResendAfterResponse(t, false), ourFirstResendAfterResponse(t, true)
	t.Logf("first resend of the response: libutp %v, or %v once the peer's FIN arrives; ours %v, or %v",
		libPlain, libFin, ourPlain, ourFin)

	// The reference implementation's half, so a change in the vendored copy
	// shows: the FIN pulls its resend forward.
	if libFin >= libPlain || libFin > 500*time.Millisecond {
		t.Errorf("libutp resent at %v with the peer's FIN against %v without; the reference "+
			"no longer times out in-flight data at the end of the peer's stream, and the "+
			"deviation should be revisited", libFin, libPlain)
	}
	// Ours: the FIN changes nothing.
	if ourFin != ourPlain {
		t.Errorf("our first resend moved from %v to %v when the peer's FIN arrived", ourPlain, ourFin)
	}
}
