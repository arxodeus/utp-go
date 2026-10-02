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

	// The read and socket event loops and the retransmission wheel register
	// when the socket is built; the connection's own loop only
	// once the SYN arrives.
	clk.AwaitParticipants(3)
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
	clk.AwaitParticipants(4)
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

// A keep-alive sent while a gap is open names the packets that really arrived.
//
// libutp's keep-alive is an acknowledgement one behind the real one, built by
// decrementing ack_nr before send_ack (utp_internal.cpp:834-844), so its
// selective ack is computed from the decremented ack_nr as well (:804-808)
// and the peer, reading bit i as ack_nr + 2 + i against the header (:1441-),
// finds the right packets. This library built the state packet first, with
// its selective ack computed from the real ack number, and then decremented
// the header: every bit was read one packet early, so the peer would have
// taken a packet it still owed as delivered, freed it, and never sent it
// again -- and this end would have waited for it forever. Found reading
// send_keep_alive line by line (the audit in LIBUTP-AUDIT.md).
func TestKeepAliveSelectiveAckMatchesItsAckNumber(t *testing.T) {
	const (
		ourSeq   = 0x4321
		peerID   = 6100
		peerSeq  = 900
		interval = defaultKeepAliveInterval
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
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCid(ctx, cid, cfg)
		accepted <- err
	}()
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	conn.takeEmitted()

	// peerSeq+1 never arrives; peerSeq+2 does. The acknowledgement stays at
	// peerSeq, with peerSeq+2 selectively acknowledged.
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 190000, 1<<20, peerSeq+2).
			WithAckNum(ourSeq - 1).WithPayload([]byte("after the gap")).Build().Encode())
	})
	conn.takeEmitted()

	clk.Advance(interval)
	emitted := conn.takeEmitted()
	if len(emitted) != 1 {
		t.Fatalf("expected one keep-alive after %v of silence, got %s", interval, describePackets(emitted))
	}
	pkt, err := DecodePacket(emitted[0])
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Header.PacketType != st_state || pkt.Header.AckNum != peerSeq-1 {
		t.Fatalf("keep-alive: %s, expected a STATE acknowledging %d", describePackets(emitted), peerSeq-1)
	}
	if pkt.Eack == nil {
		t.Fatal("the keep-alive carried no selective ack with a gap open; libutp's does")
	}
	var named []uint16
	for i, set := range pkt.Eack.Acked() {
		if set {
			named = append(named, pkt.Header.AckNum+2+uint16(i))
		}
	}
	if len(named) != 1 || named[0] != peerSeq+2 {
		t.Fatalf("the keep-alive's selective ack, read against its own ack number %d, names %v "+
			"as received; only %d arrived", pkt.Header.AckNum, named, peerSeq+2)
	}
}

// The idle timeout runs from the last packet heard, to the instant.
//
// Activity used to reset the idle timer on every packet. It now only records
// the time, and the timer, when it fires, arms again for what is left
// (connection.lastActivity). So the timer set at the handshake fires at 10
// seconds, finds a packet heard at 6, and must neither close the connection
// then nor let it outlive 16.
func TestIdleTimeoutRunsFromTheLastPacketHeard(t *testing.T) {
	const (
		ourSeq      = 0x4321
		peerID      = 6000
		peerSeq     = 900
		idleTimeout = 10 * time.Second
		heardAt     = 6 * time.Second
		slack       = time.Millisecond
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
	cfg.MaxIdleTimeout = idleTimeout

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
	var stream *UtpStream
	select {
	case stream = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the accept did not complete within 10s of the SYN")
	}
	if stream == nil {
		t.FailNow()
	}
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	setUp := clk.Now()

	clk.Advance(heardAt)
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 190000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("still here")).Build().Encode())
	})

	timedOut := func() bool {
		boxed := stream.conn.terminalErr.Load()
		return boxed != nil && boxed.err == ErrTimedOut
	}
	clk.Advance(idleTimeout - slack)
	if timedOut() {
		t.Fatalf("the connection timed out %v after the handshake, %v after the last "+
			"packet it heard; the timeout is %v", clk.Now().Sub(setUp),
			clk.Now().Sub(setUp)-heardAt, idleTimeout)
	}
	clk.Advance(slack)
	if !timedOut() {
		t.Fatalf("the connection was still open %v after the last packet it heard; "+
			"the timeout is %v", clk.Now().Sub(setUp)-heardAt, idleTimeout)
	}
}
