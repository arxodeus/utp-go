package utp

import (
	"context"
	"testing"
	"time"
)

// The keep-alive leaves one interval after the last packet sent, not up to
// two. It runs on the virtual clock at the real 29-second interval and checks
// the instant.
func TestKeepAliveLeavesOneIntervalAfterTheLastPacket(t *testing.T) {
	const (
		ourSeq    = 0x4321
		peerID    = 6000
		peerSeq   = 900
		interval  = defaultKeepAliveInterval
		quietFrom = 5 * time.Second // well away from any multiple of the interval
		// The instant is exact on the virtual clock; the tolerance need only be
		// finer than the 24-second gap between right and wrong.
		slack = time.Millisecond
	)

	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(ourSeq)()

	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }
	if cfg.KeepAliveInterval != interval {
		t.Fatalf("the default interval is %v, not libutp's 29s; this case is "+
			"written against libutp's", cfg.KeepAliveInterval)
	}

	cid := NewConnectionID(conn.peer, peerID+1, peerID)
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCID(ctx, cid, cfg)
		accepted <- err
	}()

	// The read and socket event loops and the retransmission wheel register
	// when the socket is built; the connection's own loop only
	// once the SYN arrives.
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(newPacketBuilder(stSyn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
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
		conn.inject(newPacketBuilder(stData, peerID+1, 190000, 1<<20, peerSeq+1).
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
		pkt, err := decodePacket(emitted[0], false)
		if err != nil {
			t.Fatalf("keep-alive %d does not decode: %v", i, err)
		}
		if pkt.Header.PacketType != stState || pkt.Header.AckNum != peerSeq {
			t.Fatalf("keep-alive %d: %s, expected a STATE acknowledging %d, one "+
				"behind what we have (send_keep_alive, utp_internal.cpp:834-844)",
				i, describePackets(emitted), peerSeq)
		}
		// Each keep-alive is itself the last packet sent, so the next is one
		// interval after it.
		lastSent = clk.Now()

		// Answer it, as a live peer does. The answer must not itself draw a
		// packet, or it would move lastSent.
		clk.AwaitReactionTo(func() {
			conn.inject(newPacketBuilder(stState, peerID+1, 200000, 1<<20, peerSeq+2).
				WithAckNum(ourSeq - 1).Build().Encode())
		})
		if emitted := conn.takeEmitted(); len(emitted) != 0 {
			t.Fatalf("the peer's answer to keep-alive %d drew %s", i, describePackets(emitted))
		}
	}
}

// A keep-alive sent while a gap is open names the packets that really
// arrived. It acknowledges one behind the real acknowledgement number, so
// its selective ack must be computed against that decremented number; read
// against the header, every bit would otherwise be one packet early, and the
// peer would free a packet it still owed.
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
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()
	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, peerID+1, peerID)
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCID(ctx, cid, cfg)
		accepted <- err
	}()
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(newPacketBuilder(stSyn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
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
		conn.inject(newPacketBuilder(stData, peerID+1, 190000, 1<<20, peerSeq+2).
			WithAckNum(ourSeq - 1).WithPayload([]byte("after the gap")).Build().Encode())
	})
	conn.takeEmitted()

	clk.Advance(interval)
	emitted := conn.takeEmitted()
	if len(emitted) != 1 {
		t.Fatalf("expected one keep-alive after %v of silence, got %s", interval, describePackets(emitted))
	}
	pkt, err := decodePacket(emitted[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Header.PacketType != stState || pkt.Header.AckNum != peerSeq-1 {
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

// The idle timeout runs from the last packet heard, to the instant. The
// timer set at the handshake fires at 10 seconds, finds a packet heard at 6,
// and must neither close the connection then nor let it outlive 16.
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
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }
	cfg.MaxIdleTimeout = idleTimeout

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
	var stream *Stream
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
		conn.inject(newPacketBuilder(stData, peerID+1, 190000, 1<<20, peerSeq+1).
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
