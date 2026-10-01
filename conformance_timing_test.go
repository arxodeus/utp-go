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
// and 1000ms defaults, on every run. Ours is measured on a virtual clock too,
// with the base timeout scaled down. The multiples must
// match, and the bases must be equal -- which together mean the absolute
// schedules coincide. Both halves are asserted; neither is assumed.

// scheduleLateness is how late one of our retransmissions may be: less than
// one tick of the retransmission wheel (defaultRetransmitTickInterval), which
// is its resolution. On the virtual clock that is all the lateness there is.
//
// This was a 20% tolerance while our side ran on real timers, and it hid two
// things. Under CPU load the scheduler alone used it up (first retransmissions
// at 245-249 ms against 200). And the wheel rounded every re-arm up by nearly
// a tick, so each backoff landed 25 ms later than the last -- 208, 633, 1458,
// 3084 ms -- which a percentage of a growing deadline never caught.
const scheduleLateness = defaultRetransmitTickInterval

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
	// This pins libutp's retransmission-timeout schedule. The loss probe is
	// not libutp's and would add one packet before the first timeout; it is
	// measured on its own in loss_probe_test.go.
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

		// Never early. A retransmission before the timeout has elapsed
		// resends a packet the peer was still going to acknowledge, and
		// libutp cannot do it: it compares the clock against rto_timeout
		// before declaring a timeout (utp_internal.cpp:1147-1148). To its
		// millisecond, which is also the resolution of these times.
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
// scripted transport and records when it retransmits.
//
// The peer never acknowledges anything, so every emission after the first is a
// retransmission.
//
// On a virtual clock (virtual_clock_test.go), as the corpus runs. This used to
// run on the real clock, and the schedule it measured was the machine's as
// much as the connection's: the retransmission wheel ticks every 25 ms, which
// against the 200 ms base the callers scale libutp's down to is already 12.5%,
// and the 20% tolerance left 15 ms for the scheduler. Under CPU load the first
// retransmission came at 245-249 ms, failing 4 runs in 12 before any change
// and once in each of two full runs of the package. The times come from each
// packet's header, which the connection stamps from the same clock.
func ourRetransmitSchedule(t *testing.T, cfg *ConnectionConfig, afterHandshake bool) (resends []int64, gaveUp time.Duration) {
	t.Helper()

	restore := pinRandom(initiatorConnSeed)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clk := newVirtualClock(time.Unix(1, 0))
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
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

	// The socket's wheel and its read, write and event loops, and the
	// connection: the SYN has gone out once all five are parked.
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	start := clk.Now()

	// Times since the first packet that matches, from the header stamps.
	var startMicros uint32
	var haveFirst bool
	collect := func(match func(*packet) bool) {
		for _, raw := range conn.takeEmitted() {
			pkt, err := DecodePacket(raw)
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
		// Giving up ends the connection's event loop, which leaves the
		// clock within the step that did it. ConnectWithCid reports it later,
		// from a goroutine the clock does not wait for.
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
					t.Fatal("the connection ended, and ConnectWithCid did not return")
				}
				return resends, gaveUp
			}
		}
		return resends, 0
	}

	// Complete the handshake, then write data nobody will acknowledge. The
	// SYN-ACK comes back 10 ms later: a round-trip estimate of zero would
	// leave the loss probe, which needs one, never armed.
	clk.Advance(10 * time.Millisecond)
	clk.AwaitReactionTo(func() { conn.inject(initiatorSynAck()) })
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
	clk.AwaitReactionTo(func() { _, _ = stream.Write(writeCtx, []byte("payload")) })

	// Retransmissions of the data packet specifically: the acknowledgement of
	// the SYN-ACK can go out alongside, and counting it would put every time
	// one step out.
	var firstDataSeq uint16
	var haveSeq bool
	isFirstData := func(p *packet) bool {
		if p.Header.PacketType != st_data {
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
