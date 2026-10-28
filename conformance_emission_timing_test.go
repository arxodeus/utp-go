//go:build cgo

package utp

import (
	"context"
	"testing"
	"time"

	"github.com/arxodeus/utp/internal/libutp"
)

// emissionInstants reads the instant each packet was built from the packet
// itself. The timestamp field carries the sender's clock when the header was
// assembled, and both implementations restamp it on every transmission, so
// on a virtual clock it is the emission instant exactly. The instant is fixed
// where the header is built, synchronously with the connection's processing,
// which avoids timing a packet's passage through channels.
func emissionInstants(t *testing.T, raw [][]byte, startMicros uint64) []time.Duration {
	t.Helper()
	var at []time.Duration
	for i, b := range raw {
		if len(b) < minimalHeaderSize {
			t.Fatalf("emitted packet %d is %d bytes, shorter than a header", i, len(b))
		}
		h, err := decodePacketHeader(b[:minimalHeaderSize])
		if err != nil {
			t.Fatalf("emitted packet %d does not decode: %v", i, err)
		}
		at = append(at, time.Duration(
			wrappingSubUint32(uint32(h.Timestamp), uint32(startMicros)))*time.Microsecond)
	}
	return at
}

// ourSynRetransmissions returns the instants at which this implementation
// resends a SYN nothing ever answers, on a clock the test owns.
func ourSynRetransmissions(t *testing.T, within time.Duration) []time.Duration {
	t.Helper()

	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(initiatorConnSeed)()

	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	go func() { _, _ = sock.ConnectWithCID(ctx, cid, cfg) }()

	// Four participants register on this clock: the socket's retransmission
	// wheel and its read and event loops when the socket is built, and this
	// connection's event loop when its goroutine runs.
	clk.AwaitParticipants(4)

	// The first SYN is not a retransmission; wait for it and discard it.
	// Counting may poll; the instants come from the packets.
	waitForEmitted(t, conn, 1)
	conn.takeEmitted()

	clk.Advance(within)

	// The attempt ends after three transmissions, so at most two
	// retransmissions are expected; wait for them to reach the transport.
	waitForEmitted(t, conn, 2)
	return emissionInstants(t, conn.takeEmitted(), uint64(start.UnixMicro()))
}

// waitForEmitted blocks until the scripted transport has at least n packets,
// or fails. Used only for counting.
func waitForEmitted(t *testing.T, conn *scriptedConn, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for conn.emittedCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d packets reached the transport in 10s",
				conn.emittedCount(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// libutpSynRetransmissions is the same measurement against the reference, off
// its own virtual clock and read the same way.
func libutpSynRetransmissions(t *testing.T, within time.Duration) []time.Duration {
	t.Helper()

	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.PushRandom(uint32(initiatorConnSeed))
	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	start := drv.Now()
	drv.ClearEmitted()

	// The reference driver is synchronous, so its clock is advanced in steps
	// with a timeout check after each. The step affects when it notices a
	// deadline, not the instant it stamps, which is read from the packet as
	// ours is.
	const step = 10 * time.Millisecond
	for elapsed := time.Duration(0); elapsed < within; elapsed += step {
		drv.Advance(uint64(step.Microseconds()))
		drv.CheckTimeouts()
	}
	return emissionInstants(t, drv.Emitted(), start)
}

// When this implementation sends, compared numerically with the reference
// implementation. The connection's deadlines, timers and wall clock all come
// from a clock the test owns, and the event loop, retransmission wheel and
// read loop each report when they are parked (idleBarrier), so time moves
// between reactions rather than during one. The instants are read from the
// packets.
func TestSynRetransmissionInstantsMatchLibutp(t *testing.T) {
	const within = 20 * time.Second

	ours := ourSynRetransmissions(t, within)
	theirs := libutpSynRetransmissions(t, within)

	t.Logf("ours:   %v", ours)
	t.Logf("libutp: %v", theirs)

	if len(theirs) == 0 {
		t.Fatal("libutp retransmitted nothing; there is no reference to compare against")
	}
	if len(ours) == 0 {
		t.Fatal("we retransmitted nothing; the measurement is empty")
	}
	if len(ours) != len(theirs) {
		t.Errorf("we sent %d retransmissions within %v and libutp sent %d: %v against %v",
			len(ours), within, len(theirs), ours, theirs)
	}

	n := min(len(ours), len(theirs))
	for i := 0; i < n; i++ {
		// Within one wheel tick, however many retransmissions in. A timer fires
		// on the first tick at or after its deadline, never early, and the
		// lateness does not accumulate across backoffs (timeWheel.put).
		tolerance := defaultRetransmitTickInterval
		// The reference instants carry its driver's step granularity.
		tolerance += 10 * time.Millisecond
		diff := ours[i] - theirs[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("retransmission %d: ours at %v, libutp at %v, %v apart (tolerance %v)",
				i, ours[i], theirs[i], diff, tolerance)
		}
		// And never early, the direction that matters.
		if ours[i] < theirs[i]-10*time.Millisecond {
			t.Errorf("retransmission %d: ours at %v is earlier than libutp's %v; a "+
				"retransmission before the deadline resends a packet the peer was "+
				"still going to acknowledge", i, ours[i], theirs[i])
		}
	}
}

// The barrier itself: if it silently did nothing, every instant above would
// look exact while being racy.
func TestVirtualClockHoldsTheConnectionStill(t *testing.T) {
	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(initiatorConnSeed)()

	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	go func() { _, _ = sock.ConnectWithCID(ctx, cid, cfg) }()

	clk.AwaitParticipants(4)
	waitForEmitted(t, conn, 1)
	conn.takeEmitted()

	// Real time does not move this connection's clock.
	before := clk.Now()
	time.Sleep(50 * time.Millisecond)
	if got := clk.Now(); !got.Equal(before) {
		t.Errorf("the clock moved on its own, from %v to %v", before, got)
	}

	// Nothing is retransmitted while it stands still, however long the test
	// waits in real time. The SYN's timeout is three seconds; this waits a
	// fifteenth of that and would fail if any part of the connection were
	// still on the real clock.
	time.Sleep(200 * time.Millisecond)
	if n := conn.emittedCount(); n != 0 {
		t.Errorf("%d packets were emitted with the clock stopped; some part of the "+
			"connection is still running on real time", n)
	}

	// Moving past the timeout does produce one.
	clk.Advance(4 * time.Second)
	waitForEmitted(t, conn, 1)
}

// ourReplyInstant injects one packet and returns how long after it this
// implementation answered, on a clock the test owns. The injection instant is
// the clock's when the packet is handed to the transport; the reply instant
// comes from the reply's timestamp field.
func ourReplyInstant(t *testing.T) (time.Duration, []byte) {
	t.Helper()

	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(corpusPinnedSeq)()

	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, corpusSynConnID+1, corpusSynConnID)
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCID(ctx, cid, cfg)
		accepted <- err
	}()

	// Three participants register when the socket is built: the
	// retransmission wheel and the read and event loops. The accepting
	// connection's event loop, the fourth, exists only once a SYN arrives.
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	conn.takeEmitted()

	// The handshake first, so the connection is established before the packet
	// whose reply is being timed.
	clk.AwaitReactionTo(func() {
		conn.inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
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

	// Now one data packet, timing when the acknowledgement goes out.
	// AwaitQuiet waits for the read loop, which handles the packet and sends
	// the acknowledgement itself, to park.
	injectedAt := clk.Now()
	clk.AwaitReactionTo(func() { conn.inject(fuzzPrimingPacket()) })

	emitted := conn.takeEmitted()
	if len(emitted) == 0 {
		t.Fatal("no reply to the injected packet")
	}
	instants := emissionInstants(t, emitted[:1], uint64(injectedAt.UnixMicro()))
	return instants[0], emitted[0]
}

// libutpReplyInstant is the same measurement against the reference.
func libutpReplyInstant(t *testing.T) time.Duration {
	t.Helper()

	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.PushRandom(corpusPinnedSeq)
	drv.Listen()

	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.IssueAcks()
	drv.ClearEmitted()

	injectedAt := drv.Now()
	drv.Inject(fuzzPrimingPacket())
	drv.IssueAcks()
	emitted := drv.Emitted()
	if len(emitted) == 0 {
		t.Fatal("libutp did not reply to the injected packet")
	}
	return emissionInstants(t, emitted[:1], injectedAt)[0]
}

// How long after a packet arrives the acknowledgement goes out, compared
// with the reference implementation. Both defer acknowledgements and flush
// them at the end of the read that brought the packet, so the expected answer
// for both is the same instant: a nonzero delay would mean the
// acknowledgement slipped to a later pass.
func TestReplyInstantMatchesLibutp(t *testing.T) {
	ours, raw := ourReplyInstant(t)
	theirs := libutpReplyInstant(t)

	h, err := decodePacketHeader(raw[:minimalHeaderSize])
	if err != nil {
		t.Fatalf("our reply does not decode: %v", err)
	}
	t.Logf("ours:   replied %v after the packet arrived (%s)", ours, h.PacketType.String())
	t.Logf("libutp: replied %v after the packet arrived", theirs)

	if ours != 0 {
		t.Errorf("we answered %v after the packet arrived; the acknowledgement is "+
			"deferred to the end of the pass that received it, so it should carry "+
			"the same instant", ours)
	}
	if ours != theirs {
		t.Errorf("we answered %v after arrival and libutp %v", ours, theirs)
	}
}
