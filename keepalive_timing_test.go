package utp_go

import (
	"context"
	"testing"
	"time"
)

// The keep-alive leaves one interval after the last packet, not up to two.
//
// libutp checks `current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every
// timeout pass (utp_internal.cpp:1271-1274). This library checked the same
// condition only on a ticker at the interval, counted from connection start,
// so a connection that went quiet 5 seconds after a tick failed the check at
// the next one and sent at the one after: 53 seconds of silence where libutp
// allows 29. TestKeepAlive could not see that -- it gives the keep-alive six
// intervals to turn up -- so this runs on the virtual clock at libutp's real
// interval and asserts the instant.
func TestKeepAliveLeavesOneIntervalAfterTheLastPacket(t *testing.T) {
	const (
		ourSeq    = 0x4321
		peerID    = 6000
		peerSeq   = 900
		interval  = defaultKeepAliveInterval
		quietFrom = 5 * time.Second // well away from any multiple of the interval
		// The instant is exact on the virtual clock; this only has to be
		// finer than the gap between right and wrong, which is 24 seconds.
		slack = time.Millisecond
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
	if cfg.KeepAliveInterval != interval {
		t.Fatalf("the default interval is %v, not libutp's 29s; this case is "+
			"written against libutp's", cfg.KeepAliveInterval)
	}

	cid := NewConnectionId(conn.peer, peerID+1, peerID)
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCid(ctx, cid, cfg)
		accepted <- err
	}()

	// The read, write and socket event loops and the retransmission wheel
	// register when the socket is built; the connection's own loop only
	// once the SYN arrives.
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the accept neither completed nor failed within 10s of the SYN")
	}
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	conn.takeEmitted()

	// Speak once, 5 seconds in: the acknowledgement for this is the last
	// packet the connection sends before it goes quiet.
	clk.Advance(quietFrom)
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 190000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("last word")).Build().Encode())
	})
	if n := len(conn.takeEmitted()); n != 1 {
		t.Fatalf("expected one acknowledgement for the data, got %d packets", n)
	}
	lastSent := clk.Now()

	for i := 1; i <= 3; i++ {
		clk.Advance(interval - slack)
		if emitted := conn.takeEmitted(); len(emitted) != 0 {
			t.Fatalf("keep-alive %d: sent %s after %v of silence, before the interval",
				i, describePackets(emitted), clk.Now().Sub(lastSent))
		}
		clk.Advance(slack)
		emitted := conn.takeEmitted()
		if len(emitted) != 1 {
			t.Fatalf("keep-alive %d: %d packets after exactly %v of silence, expected "+
				"one keep-alive (libutp: current_ms - last_sent_packet >= "+
				"KEEPALIVE_INTERVAL, utp_internal.cpp:1272)",
				i, len(emitted), clk.Now().Sub(lastSent))
		}
		pkt, err := DecodePacket(emitted[0])
		if err != nil {
			t.Fatalf("keep-alive %d does not decode: %v", i, err)
		}
		if pkt.Header.PacketType != st_state || pkt.Header.AckNum != peerSeq {
			t.Fatalf("keep-alive %d: %s, expected a STATE acknowledging %d, one "+
				"behind what we have (send_keep_alive, utp_internal.cpp:834-844)",
				i, describePackets(emitted), peerSeq)
		}
		// Each keep-alive is itself the last packet sent, so the next is one
		// interval after it.
		lastSent = clk.Now()

		// Answer it, as a live peer does. A peer that never speaks is dead,
		// and the idle timeout closes the connection 60 seconds after it last
		// heard anything -- which, left alone, is between the second
		// keep-alive and the third. The answer must not itself draw a
		// packet, or it would move lastSent.
		clk.AwaitReactionTo(func() {
			conn.inject(NewPacketBuilder(st_state, peerID+1, 200000, 1<<20, peerSeq+2).
				WithAckNum(ourSeq - 1).Build().Encode())
		})
		if emitted := conn.takeEmitted(); len(emitted) != 0 {
			t.Fatalf("the peer's answer to keep-alive %d drew %s", i, describePackets(emitted))
		}
	}
}
