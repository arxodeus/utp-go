//go:build cgo

package utp_go

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// Retransmission timing, compared against libutp.
//
// This is the gap CONFORMANCE.md has recorded since M2: the corpus compares
// *what* each implementation emits, never *when*. A retransmission schedule
// that backs off differently is invisible to it, and to the differential
// fuzzer, and would show up in the wild as a connection that either gives up
// too early or hammers a dead path.
//
// The obstacle was always our side's clock: libutp runs on a virtual clock the
// driver controls, ours runs on the real one with real timers, so the two
// cannot be stepped together. The way round it is to compare the schedule's
// *shape* rather than its absolute times. Both implementations parameterise
// the backoff on a base timeout and double from there, so the schedule is a
// sequence of multiples of that base -- and multiples are comparable across
// two different time scales.
//
// So: libutp's schedule is measured on its virtual clock, at its real 3000ms
// and 1000ms defaults, on every run. Ours is measured on the real clock with
// the base timeout scaled down to keep the test fast. The multiples must
// match, and the bases must be equal -- which together mean the absolute
// schedules coincide. Both halves are asserted; neither is assumed.

// scheduleTolerance is how far a measured retransmission may fall from its
// expected multiple.
//
// Ours is generous because our side runs on real timers under a test runner,
// and because the retransmission wheel has a resolution of its own
// (defaultRetransmitTickInterval, 25ms) which rounds every arming up to a
// tick. That makes each retransmission systematically a little late -- at the
// real 1000ms RTO it is under 2.5%, and at the scaled-down base used here it
// is larger, which is why the base is chosen well above the tick interval.
//
// A 20% window absorbs that without admitting a schedule that backs off by a
// different factor, which is what this is looking for: the gaps between
// consecutive retransmissions double, so an off-by-one in the backoff is a
// 100% error, not a 20% one.
const scheduleTolerance = 0.20

// libutpRetransmitSchedule drives libutp until it stops retransmitting, and
// returns the times (in milliseconds from the first transmission) at which it
// resent, plus the time it gave up.
//
// The clock is virtual and driven in 10ms steps, so this is exact to 10ms and
// takes microseconds of real time.
func libutpRetransmitSchedule(t *testing.T, afterHandshake bool) (resends []int64, gaveUpAt int64) {
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
	drv.ClearEmitted()

	if afterHandshake {
		// Answer the SYN, then send data that is never acknowledged. The
		// SYN-ACK arrives with the virtual clock unmoved, so libutp's first
		// RTT sample is zero and its RTO falls to the 1000ms floor -- which
		// is the case worth pinning, since it is what any connection whose
		// peer stops answering ends up at.
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
		if st := drv.State(); st == libutp.StateError || st == libutp.StateDestroyed {
			gaveUpAt = elapsed
			return resends, gaveUpAt
		}
	}
	return resends, 0
}

// multiples turns a list of times into multiples of a base, rounded to the
// nearest whole number. A schedule of 1000, 3000, 7000 against a base of 1000
// becomes 1, 3, 7 -- the shape that is comparable across time scales.
func multiples(times []int64, baseMillis int64) []int64 {
	out := make([]int64, 0, len(times))
	for _, t := range times {
		out = append(out, (t+baseMillis/2)/baseMillis)
	}
	return out
}

// libutp's SYN backoff, and ours.
//
// libutp gives up when retransmit_count reaches 2 in CS_SYN_SENT, which is
// three transmissions in total; the timeout starts at 3000ms and doubles
// (utp_internal.cpp:1179 applied at :1203, initial value at :2762).
// DEVIATIONS.md records that as matched. This measures it.
func TestConformanceSynRetransmitSchedule(t *testing.T) {
	resends, gaveUpAt := libutpRetransmitSchedule(t, false)
	const libutpBase = 3000

	got := multiples(resends, libutpBase)
	t.Logf("libutp SYN retransmissions at %v ms = %v x its %dms base; gave up at %dms (%dx)",
		resends, got, libutpBase, gaveUpAt, (gaveUpAt+libutpBase/2)/libutpBase)

	// Doubling from the base gives cumulative 1, 3, 7, ... Two retransmissions
	// then give up is three transmissions in total.
	want := []int64{1, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("libutp retransmitted the SYN at %v x its base, expected %v. "+
			"The schedule this test compares against has changed; re-derive it before "+
			"changing anything here.", got, want)
	}
	if gaveUpMultiple := (gaveUpAt + libutpBase/2) / libutpBase; gaveUpMultiple != 7 {
		t.Fatalf("libutp gave up at %dx its base, expected 7x", gaveUpMultiple)
	}

	// Ours, on the real clock with the base scaled down.
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

	// The multiples matching only means the absolute schedules match if the
	// bases do. libutp's is a hard-coded 3000ms; ours is configurable, and its
	// default must equal it.
	if got, want := NewConnectionConfig().InitialTimeout, 3000*time.Millisecond; got != want {
		t.Errorf("our default InitialTimeout is %v, libutp's SYN timeout is %v; the schedules "+
			"have the same shape but not the same times", got, want)
	}
}

// The data retransmission schedule, which is the one that matters in a
// transfer: libutp doubles its RTO from a 1000ms floor
// (rto = max(rtt + rtt_var*4, 1000), utp_internal.cpp:1380).
func TestConformanceDataRetransmitSchedule(t *testing.T) {
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
// multiples of a base, within tolerance.
func assertScheduleMatches(t *testing.T, what string, wantMultiples []int64, got []int64, baseMillis int64) {
	t.Helper()
	if len(got) < len(wantMultiples) {
		t.Fatalf("we retransmitted the %s %d times at %v ms; libutp retransmits %d times, at %v "+
			"x the base timeout", what, len(got), got, len(wantMultiples), wantMultiples)
	}
	for i, mult := range wantMultiples {
		expected := float64(mult * baseMillis)
		actual := float64(got[i])

		// Never early. A retransmission before the timeout has elapsed
		// resends a packet the peer was still going to acknowledge, and
		// libutp cannot do it: it compares the clock against rto_timeout
		// before declaring a timeout (utp_internal.cpp:1147-1148). Asserted
		// separately from the tolerance, which is two-sided.
		if actual < expected*0.95 {
			t.Errorf("%s retransmission %d was at %vms, before its %dx base = %vms. "+
				"Retransmitting early resends packets the peer was still going to ack.",
				what, i, actual, mult, expected)
		}
		delta := actual - expected
		if delta < 0 {
			delta = -delta
		}
		if delta/expected > scheduleTolerance {
			t.Errorf("%s retransmission %d was at %vms; libutp's is at %dx the base = %vms "+
				"(%.0f%% off, tolerance %.0f%%). Full schedule: ours %v, libutp's multiples %v",
				what, i, actual, mult, expected, 100*delta/expected, 100*scheduleTolerance,
				got, wantMultiples)
		}
	}
}

// ourRetransmitSchedule drives this implementation as the initiator over a
// scripted transport and records when it retransmits.
//
// The peer never acknowledges anything, so every emission after the first is a
// retransmission.
func ourRetransmitSchedule(t *testing.T, cfg *ConnectionConfig, afterHandshake bool) (resends []int64, gaveUp time.Duration) {
	t.Helper()

	restore := pinRandom(initiatorConnSeed)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	connected := make(chan *UtpStream, 1)
	connectErr := make(chan error, 1)
	go func() {
		stream, err := sock.ConnectWithCid(ctx, cid, cfg)
		if err != nil {
			connectErr <- err
			return
		}
		connected <- stream
	}()

	// Wait for the SYN.
	waitFor(t, 2*time.Second, func() bool { return conn.emittedCount() > 0 })

	if !afterHandshake {
		start := time.Now()
		baseline := conn.emittedCount()
		deadline := start.Add(cfg.InitialTimeout * 10)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
			if n := conn.emittedCount(); n > baseline {
				resends = append(resends, time.Since(start).Milliseconds())
				baseline = n
			}
			select {
			case err := <-connectErr:
				gaveUp = time.Since(start)
				_ = err
				return resends, gaveUp
			default:
			}
		}
		return resends, 0
	}

	// Complete the handshake, then write data nobody will acknowledge.
	conn.inject(initiatorSynAck())
	var stream *UtpStream
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
	go func() { _, _ = stream.Write(writeCtx, []byte("payload")) }()

	// Record retransmissions of the data packet specifically.
	//
	// Counting emissions would be wrong: the acknowledgement of the SYN-ACK
	// can land in the same window and would be counted as the first
	// retransmission, putting every subsequent time one step out. Matching on
	// the data packet's own sequence number is exact.
	type timed struct {
		at  time.Duration
		pkt *packet
	}
	var seen []timed
	var firstDataSeq uint16
	var haveFirst bool
	var start time.Time

	deadline := time.Now().Add(cfg.MinTimeout * 20)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
		now := time.Now()
		for _, raw := range conn.takeEmitted() {
			pkt, err := DecodePacket(raw)
			if err != nil {
				continue
			}
			if pkt.Header.PacketType != st_data {
				continue
			}
			if !haveFirst {
				firstDataSeq = pkt.Header.SeqNum
				haveFirst = true
				start = now
				continue
			}
			if pkt.Header.SeqNum == firstDataSeq {
				seen = append(seen, timed{at: now.Sub(start), pkt: pkt})
			}
		}
	}

	for _, s := range seen {
		resends = append(resends, s.at.Milliseconds())
	}
	return resends, 0
}
