package utp_go

import (
	"context"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// emissionInstants reads the instant each packet was built from the packet
// itself.
//
// The timestamp field carries the sender's clock at the moment the header was
// assembled, and both implementations re-stamp it on every transmission --
// ours in retransmit and synPacket, libutp in send_data. So when both sides
// are on a virtual clock the field *is* the emission instant, exactly, with
// no sampling and no quantisation.
//
// Reading it from the packet rather than timing its arrival also sidesteps a
// question with no clean answer: a packet leaves a connection through a
// channel, and "every goroutine is parked" is not the same as "nothing is in
// flight" while a buffered handoff is outstanding. The instant is decided
// where the header is built, which is synchronous with the connection's own
// processing, and that is also where libutp's sendto callback fires.
func emissionInstants(t *testing.T, raw [][]byte, startMicros uint64) []time.Duration {
	t.Helper()
	var at []time.Duration
	for i, b := range raw {
		if len(b) < MINIMAL_HEADER_SIZE {
			t.Fatalf("emitted packet %d is %d bytes, shorter than a header", i, len(b))
		}
		h, err := DecodePacketHeader(b[:MINIMAL_HEADER_SIZE])
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
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionId(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	go func() { _, _ = sock.ConnectWithCid(ctx, cid, cfg) }()

	// Three participants register on this clock: the socket's retransmission
	// wheel and its write loop, both when the socket is built, and this
	// connection's event loop when its goroutine runs.
	clk.AwaitParticipants(3)

	// The first SYN is not a retransmission; wait for it and discard it.
	// Counting is allowed to poll -- it is the *instants* that must not be
	// inferred from wall time, and those come from the packets.
	waitForEmitted(t, conn, 1)
	conn.takeEmitted()

	clk.Advance(within)

	// libutp gives up after three attempts, so at most two retransmissions
	// are expected; wait for them to reach the transport before reading.
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

	// libutp's driver is synchronous: it only acts when told to, so its clock
	// is advanced in steps with a timeout check after each. The step affects
	// when libutp *notices* a deadline, not the instant it stamps, which is
	// read from the packet exactly as ours is.
	const step = 10 * time.Millisecond
	for elapsed := time.Duration(0); elapsed < within; elapsed += step {
		drv.Advance(uint64(step.Microseconds()))
		drv.CheckTimeouts()
	}
	return emissionInstants(t, drv.Emitted(), start)
}

// *When* this implementation sends, compared against libutp as numbers.
//
// Every other comparison in this corpus is about what a packet contained.
// Timing was compared once, for retransmission, by running libutp on its
// virtual clock and this library on the real one and checking the two
// schedules had the same *shape* -- because nothing could ask at what instant
// our side sent anything. The answer depended on when a goroutine happened to
// be scheduled, which is also why several tests here wait for quiet rather
// than for an event, and why two of this session's defects turned out to be
// timing assumptions in the harness rather than faults in the library.
//
// Clock and IdleBarrier close that. The connection's deadlines, timers and
// wall clock all come from a clock this test owns, and the event loop, the
// retransmission wheel and the socket's write loop each report when they are
// parked, so time moves between reactions rather than during one. The
// instants are then read from the packets, exactly.
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
		// The tolerance grows by one wheel tick per retransmission, and that
		// is a property this measurement exposed rather than a fudge.
		//
		// The retransmission wheel rounds a delay up to a whole tick and so
		// fires up to one tick late -- deliberately, because firing early
		// resends a packet the peer was still going to acknowledge. Each
		// backoff then re-arms *relative to the moment the last one fired*,
		// so the lateness carries forward and the next deadline inherits it.
		// libutp cannot accumulate this: it compares the clock against an
		// absolute rto_timeout (utp_internal.cpp:1147-1148) rather than
		// trusting a timer, so each deadline is independent of the last.
		//
		// Measured here as 3.025s and 9.05s against libutp's 3.0s and 9.0s:
		// one tick, then two. The drift is bounded by the number of
		// retransmissions times the 25ms tick, one-directional, and in the
		// safe direction. It is recorded in KNOWN-LIMITATIONS.md rather than
		// hidden in a round number.
		tolerance := time.Duration(i+1) * defaultRetransmitTickInterval
		// libutp's own instants carry its driver's stepping granularity.
		tolerance += 10 * time.Millisecond
		diff := ours[i] - theirs[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("retransmission %d: ours at %v, libutp at %v, %v apart (tolerance %v)",
				i, ours[i], theirs[i], diff, tolerance)
		}
		// And never early: that is the direction that matters.
		if ours[i] < theirs[i]-10*time.Millisecond {
			t.Errorf("retransmission %d: ours at %v is earlier than libutp's %v; a "+
				"retransmission before the deadline resends a packet the peer was "+
				"still going to acknowledge", i, ours[i], theirs[i])
		}
	}
}

// The barrier itself, asserted directly: everything above depends on it, and
// a barrier that silently did nothing would leave every instant looking exact
// while being as racy as before.
func TestVirtualClockHoldsTheConnectionStill(t *testing.T) {
	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(initiatorConnSeed)()

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionId(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	go func() { _, _ = sock.ConnectWithCid(ctx, cid, cfg) }()

	clk.AwaitParticipants(3)
	waitForEmitted(t, conn, 1)
	conn.takeEmitted()

	// Real time does not move this connection's clock.
	before := clk.Now()
	time.Sleep(50 * time.Millisecond)
	if got := clk.Now(); !got.Equal(before) {
		t.Errorf("the clock moved on its own, from %v to %v", before, got)
	}

	// And nothing is retransmitted while it stands still, however long the
	// test waits in the real world. The SYN's timeout is three seconds; this
	// waits a fifteenth of that and would fail outright if any part of the
	// connection were still on the real clock.
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
// implementation answered, measured on a clock the test owns.
//
// The injection point is the moment the clock is at when the packet is handed
// to the transport; the reply instant comes from the reply's own timestamp
// field. Both are exact.
func ourReplyInstant(t *testing.T) (time.Duration, []byte) {
	t.Helper()

	start := time.Unix(0, 0).Add(time.Hour)
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(corpusPinnedSeq)()

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionId(conn.peer, corpusSynConnID+1, corpusSynConnID)
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.AcceptWithCid(ctx, cid, cfg)
		accepted <- err
	}()

	// Four participants register when the socket is built: the retransmission
	// wheel and the read, write and event loops. An accepting connection does
	// not exist yet -- AcceptWithCid parks until a SYN arrives, and the event
	// loop that would be the fifth is created only then.
	clk.AwaitParticipants(4)
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
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	conn.takeEmitted()

	// Now one data packet, and the question is when the acknowledgement goes
	// out. AwaitQuiet is the whole inbound chain settling: read loop, socket
	// event loop, connection event loop, write loop -- each of which reports
	// when it parks, and each hop of which is accounted for as a handoff.
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
// against libutp.
//
// This is the inbound half of the timing question, and until the socket's
// read and event loops became barrier participants it could not be asked at
// all: a test could inject a packet and advance the clock with no guarantee
// the injection had even been processed first.
//
// Both implementations defer acknowledgements and flush them when something
// external says so -- libutp when its embedder calls utp_issue_deferred_acks,
// this library at the end of the event-loop pass that received the packet.
// So the expected answer for both is *the same instant*: neither waits, and a
// non-zero delay on either side would mean the acknowledgement had slipped to
// a later pass than the packet that prompted it.
func TestReplyInstantMatchesLibutp(t *testing.T) {
	ours, raw := ourReplyInstant(t)
	theirs := libutpReplyInstant(t)

	h, err := DecodePacketHeader(raw[:MINIMAL_HEADER_SIZE])
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
