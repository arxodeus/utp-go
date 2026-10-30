package utp

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Retransmission timing, compared with the reference implementation.
//
// The corpus compares what each implementation emits, not when. Here the
// reference implementation's schedule is measured on its virtual clock at
// its real 3000ms and 1000ms defaults, and ours on a virtual clock with the
// base timeout scaled down. The schedules, as multiples of the base, must
// match, and the default bases must be equal, which together mean the
// absolute schedules coincide.

// scheduleLateness is how late one of our retransmissions may be: less than
// one tick of the retransmission wheel (defaultRetransmitTickInterval), its
// resolution. On the virtual clock that is all the lateness there is.
const scheduleLateness = defaultRetransmitTickInterval

// libutpRetransmitSchedule drives the reference implementation until it stops
// retransmitting, and returns the times, in milliseconds from the first
// transmission, at which it resent, plus the time it gave up. The virtual
// clock is driven in 10ms steps.
func libutpRetransmitSchedule(t *testing.T, afterHandshake bool) (resends []int64, gaveUpAt int64) {
	t.Helper()

	drv, err := newRefDriver(t, 1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.PushRandom(uint32(initiatorConnSeed))
	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	drv.ClearEmitted()

	if afterHandshake {
		// Answer the SYN, then send data that is never acknowledged. The SYN-ACK
		// arrives with the virtual clock unmoved, so the first RTT sample is
		// zero and the RTO falls to its 1000ms floor, where any connection
		// whose peer stops answering ends up.
		drv.Inject(initiatorSynAck())
		drv.IssueAcks()
		drv.ClearEmitted()
		if _, err := drv.Write([]byte("payload")); err != nil {
			t.Fatalf("libutp write: %v", err)
		}
		drv.IssueAcks()
		drv.ClearEmitted()
	}

	start := drv.Now()
	const stepMicros = 10_000
	for i := 0; i < 4000; i++ {
		drv.Advance(stepMicros)
		drv.CheckTimeouts()
		elapsed := int64(drv.Now()-start) / 1000
		if len(drv.Emitted()) > 0 {
			resends = append(resends, elapsed)
			drv.ClearEmitted()
		}
		if st := drv.State(); st == refStateError || st == refStateDestroyed {
			gaveUpAt = elapsed
			return resends, gaveUpAt
		}
	}
	return resends, 0
}

// multiples turns a list of times into multiples of a base, rounded to the
// nearest whole number: 1000, 3000, 7000 against 1000 becomes 1, 3, 7, a
// shape comparable across time scales.
func multiples(times []int64, baseMillis int64) []int64 {
	out := make([]int64, 0, len(times))
	for _, t := range times {
		out = append(out, (t+baseMillis/2)/baseMillis)
	}
	return out
}

// The SYN backoff: three transmissions in total, the timeout starting at
// 3000ms and doubling.
func TestConformanceSynRetransmitSchedule(t *testing.T) {
	resends, gaveUpAt := libutpRetransmitSchedule(t, false)
	const libutpBase = 3000

	got := multiples(resends, libutpBase)
	t.Logf("libutp SYN retransmissions at %v ms = %v x its %dms base; gave up at %dms (%dx)",
		resends, got, libutpBase, gaveUpAt, (gaveUpAt+libutpBase/2)/libutpBase)

	// Doubling from the base gives cumulative 1, 3, 7, ...; two
	// retransmissions then giving up is three transmissions in total.
	want := []int64{1, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("libutp retransmitted the SYN at %v x its base, expected %v. "+
			"The schedule this test compares against has changed; re-derive it before "+
			"changing anything here.", got, want)
	}
	if gaveUpMultiple := (gaveUpAt + libutpBase/2) / libutpBase; gaveUpMultiple != 7 {
		t.Fatalf("libutp gave up at %dx its base, expected 7x", gaveUpMultiple)
	}

	// Ours, with the base scaled down.
	const ourBase = 200 * time.Millisecond
	cfg := NewConnectionConfig()
	cfg.InitialTimeout = ourBase
	cfg.MinTimeout = ourBase
	cfg.MaxTimeout = 10 * time.Second

	ourResends, ourGaveUp := ourRetransmitSchedule(t, cfg, false)
	ourMultiples := multiples(ourResends, int64(ourBase/time.Millisecond))
	t.Logf("our SYN retransmissions at %v ms = %v x our %v base; gave up at %v",
		ourResends, ourMultiples, ourBase, ourGaveUp)

	assertScheduleMatches(t, "SYN", want, ourResends, int64(ourBase/time.Millisecond))

	// Matching multiples imply matching absolute schedules only if the bases
	// match: the reference uses 3000ms, and our default must equal it.
	if got, want := NewConnectionConfig().InitialTimeout, 3000*time.Millisecond; got != want {
		t.Errorf("our default InitialTimeout is %v, libutp's SYN timeout is %v; the schedules "+
			"have the same shape but not the same times", got, want)
	}
}

// The data retransmission schedule, which matters in a transfer: the RTO
// doubles from a 1000ms floor (rto = max(rtt + rtt_var*4, 1000)).
func TestConformanceDataRetransmitSchedule(t *testing.T) {
	// The loss probe would add one packet before the first timeout; it is
	// tested on its own in loss_probe_test.go.
	withoutLossProbe(t)
	resends, _ := libutpRetransmitSchedule(t, true)
	const libutpBase = 1000

	got := multiples(resends, libutpBase)
	t.Logf("libutp data retransmissions at %v ms = %v x its %dms floor", resends, got, libutpBase)

	want := []int64{1, 3, 7, 15}
	if len(got) < len(want) {
		t.Fatalf("libutp retransmitted %d times, expected at least %d: %v", len(got), len(want), got)
	}
	got = got[:len(want)]
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("libutp retransmitted data at %v x its floor, expected %v. The schedule this "+
			"test compares against has changed; re-derive it before changing anything here.",
			got, want)
	}

	const ourBase = 200 * time.Millisecond
	cfg := NewConnectionConfig()
	cfg.InitialTimeout = ourBase
	cfg.MinTimeout = ourBase
	cfg.MaxTimeout = 10 * time.Second

	ourResends, _ := ourRetransmitSchedule(t, cfg, true)
	t.Logf("our data retransmissions at %v ms = %v x our %v floor",
		ourResends, multiples(ourResends, int64(ourBase/time.Millisecond)), ourBase)

	assertScheduleMatches(t, "data", want, ourResends, int64(ourBase/time.Millisecond))

	if got, want := NewConnectionConfig().MinTimeout, 1000*time.Millisecond; got != want {
		t.Errorf("our RTO floor is %v, libutp's is %v; the schedules have the same shape but "+
			"not the same times", got, want)
	}
}

// assertScheduleMatches checks that measured times land on the expected
// multiples of a base: never early, and less than scheduleLateness late.
func assertScheduleMatches(t *testing.T, what string, wantMultiples []int64, got []int64, baseMillis int64) {
	t.Helper()
	if len(got) < len(wantMultiples) {
		t.Fatalf("we retransmitted the %s %d times at %v ms; libutp retransmits %d times, at %v "+
			"x the base timeout", what, len(got), got, len(wantMultiples), wantMultiples)
	}
	for i, mult := range wantMultiples {
		expected := float64(mult * baseMillis)
		actual := float64(got[i])

		// Never early: a retransmission before the timeout has elapsed resends
		// a packet the peer was still going to acknowledge. To the
		// millisecond, the resolution of these times.
		if actual < expected-1 {
			t.Errorf("%s retransmission %d was at %vms, before its %dx base = %vms. "+
				"Retransmitting early resends packets the peer was still going to ack.",
				what, i, actual, mult, expected)
		}
		if late := actual - expected; late >= float64(scheduleLateness.Milliseconds()) {
			t.Errorf("%s retransmission %d was at %vms; libutp's is at %dx the base = %vms, "+
				"%vms late against a resolution of %v. Full schedule: ours %v, libutp's multiples %v",
				what, i, actual, mult, expected, late, scheduleLateness, got, wantMultiples)
		}
	}
}

// ourRetransmitSchedule drives this implementation as the initiator over a
// scripted transport on a virtual clock (virtual_clock_test.go) and records
// when it retransmits. The peer never acknowledges anything, so every
// emission after the first is a retransmission. The times come from each
// packet's header, stamped from the same clock.
func ourRetransmitSchedule(t *testing.T, cfg *ConnectionConfig, afterHandshake bool) (resends []int64, gaveUp time.Duration) {
	t.Helper()

	restore := pinRandom(initiatorConnSeed)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clk := newVirtualClock(time.Unix(1, 0))
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	conn := newScriptedConn()
	defer conn.Close()
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))
	defer sock.Close()

	cid := NewConnectionID(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	connected := make(chan *Stream, 1)
	connectErr := make(chan error, 1)
	go func() {
		stream, err := sock.ConnectWithCID(ctx, cid, cfg)
		if err != nil {
			connectErr <- err
			return
		}
		connected <- stream
	}()

	// The socket's wheel, read and event loops, and the connection: the SYN
	// has gone out once all four are parked.
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	start := clk.Now()

	// Times since the first packet that matches, from the header stamps.
	var startMicros uint32
	var haveFirst bool
	collect := func(match func(*packet) bool) {
		for _, raw := range conn.takeEmitted() {
			pkt, err := decodePacket(raw, false)
			if err != nil || !match(pkt) {
				continue
			}
			if !haveFirst {
				startMicros, haveFirst = uint32(pkt.Header.Timestamp), true
				continue
			}
			since := time.Duration(wrappingSubUint32(uint32(pkt.Header.Timestamp), startMicros)) * time.Microsecond
			resends = append(resends, since.Milliseconds())
		}
	}
	const step = 5 * time.Millisecond

	if !afterHandshake {
		// Giving up ends the connection's event loop within the step that did
		// it. ConnectWithCID reports it later, from a goroutine the clock does
		// not wait for.
		participants := clk.Participants()
		any := func(*packet) bool { return true }
		for clk.Now().Sub(start) < cfg.InitialTimeout*10 {
			clk.Advance(step)
			collect(any)
			if clk.Participants() < participants {
				gaveUp = clk.Now().Sub(start)
				select {
				case <-connectErr:
				case <-connected:
					t.Fatal("the connection ended, and connected")
				case <-time.After(5 * time.Second):
					t.Fatal("the connection ended, and ConnectWithCID did not return")
				}
				return resends, gaveUp
			}
		}
		return resends, 0
	}

	// Complete the handshake, then write data nobody acknowledges. The
	// SYN-ACK comes back 10 ms later, so there is a round-trip estimate for
	// the loss probe.
	clk.Advance(10 * time.Millisecond)
	clk.AwaitReactionTo(func() { conn.inject(initiatorSynAck()) })
	var stream *Stream
	select {
	case stream = <-connected:
	case err := <-connectErr:
		t.Fatalf("connect failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("connect never completed")
	}
	conn.takeEmitted()
	writeCtx, writeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer writeCancel()
	clk.AwaitReactionTo(func() { _, _ = stream.Write(writeCtx, []byte("payload")) })

	// Count retransmissions of the data packet only: the acknowledgement of
	// the SYN-ACK can go out alongside.
	var firstDataSeq uint16
	var haveSeq bool
	isFirstData := func(p *packet) bool {
		if p.Header.PacketType != stData {
			return false
		}
		if !haveSeq {
			firstDataSeq, haveSeq = p.Header.SeqNum, true
		}
		return p.Header.SeqNum == firstDataSeq
	}
	for clk.Now().Sub(start) < cfg.MinTimeout*20 {
		clk.Advance(step)
		collect(isFirstData)
	}
	return resends, 0
}
