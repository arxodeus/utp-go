//go:build cgo

package utp_go

import (
	"context"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// When the peer's stream ends, libutp arms its retransmission timeout
// `min(rto * 3, 60)` milliseconds out (utp_internal.cpp:2358). The RTO is
// never below 1000 ms (:1380), so that is always 60 ms, and if anything is in
// flight check_timeouts then treats it as a timeout: every packet marked for
// resend, the window cut to one packet, slow start, and the oldest resent
// (:1144-1235). A peer that half-closes while libutp is still sending -- a
// request followed by a response -- costs libutp its window.
//
// This library does not copy it (DEVIATIONS.md, "Reaching the end of the
// peer's stream does not time out what is in flight"). Both halves are
// measured here: libutp's resend moves from its timeout to its next timeout
// pass after the FIN, and ours stays where it was.

// libutpFirstResendAfterResponse is how long after sending its response
// libutp first resends it, with the peer's FIN arriving or not.
func libutpFirstResendAfterResponse(t *testing.T, peerFin bool) time.Duration {
	t.Helper()
	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.PushRandom(corpusPinnedSeq)
	drv.Listen()
	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.Inject(NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("request")).Build().Encode())
	drv.IssueAcks()
	drv.ClearEmitted()
	if _, err := drv.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	drv.IssueAcks()
	drv.ClearEmitted()
	if peerFin {
		drv.Inject(NewPacketBuilder(st_fin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
			WithAckNum(corpusPinnedSeq - 1).Build().Encode())
		drv.IssueAcks()
		drv.ClearEmitted()
	}
	for ms := 100; ms <= 8000; ms += 100 {
		drv.Advance(100_000)
		drv.CheckTimeouts()
		drv.IssueAcks()
		for _, raw := range drv.Emitted() {
			if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_data {
				return time.Duration(ms) * time.Millisecond
			}
		}
		drv.ClearEmitted()
	}
	t.Fatal("libutp never resent its response")
	return 0
}

// ourFirstResendAfterResponse is the same, for this library on a virtual
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
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	stream := <-accepted
	if stream == nil {
		t.FailNow()
	}
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 190000, 1<<20, peerSeq+1).
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
		if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_data {
			sentSeq = p.Header.SeqNum
		}
	}
	if sentSeq == 0 {
		t.Fatal("the response was not sent")
	}
	if peerFin {
		clk.AwaitReactionTo(func() {
			conn.inject(NewPacketBuilder(st_fin, peerID+1, 200000, 1<<20, peerSeq+2).
				WithAckNum(ourSeq - 1).Build().Encode())
		})
		conn.takeEmitted()
	}
	for ms := 100; ms <= 8000; ms += 100 {
		clk.Advance(100 * time.Millisecond)
		for _, raw := range conn.takeEmitted() {
			if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_data && p.Header.SeqNum == sentSeq {
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

	// libutp's half, so a change in the vendored copy shows: the FIN pulls
	// its resend forward to its next timeout pass.
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
