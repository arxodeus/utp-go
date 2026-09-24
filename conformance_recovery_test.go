//go:build cgo

package utp_go

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// Loss recovery, compared against libutp on a virtual clock.
//
// COMPATIBILITY.md rated four of these "cited": read against utp_internal.cpp
// and matched by hand, which is how the selective-ack bit reversal survived.
// These drive both implementations through the same situations and compare
// what each does -- when it resends, and which packets.
//
// The corpus compares transcripts field by field, and cannot here: libutp's
// window opens at one packet and ours at two (DEVIATIONS.md), so after the
// first round trip the two are sending different numbers of packets, and
// every packet after that has a different sequence number on each side.
// These cases drive each side separately, in lockstep on the clock, and
// compare the decision, not the bytes: how long until a resend, and which
// packet it was relative to that side's own oldest outstanding one.

// recoveryRun is both implementations after the handshake, driven separately.
type recoveryRun struct {
	*initiatorRun
	t *testing.T
	// sent is every data sequence number each side has put on the wire,
	// first transmissions only.
	libutpSent, oursSent map[uint16]bool
	libutpHigh, oursHigh uint16
}

// newRecoveryRun completes the handshake, the SYN-ACK arriving synDelay
// after the SYN.
func newRecoveryRun(t *testing.T, synDelay time.Duration) *recoveryRun {
	t.Helper()
	return newRecoveryRunMTU(t, synDelay, 0)
}

// newRecoveryRunMTU is newRecoveryRun with libutp's reported MTU set; see
// newInitiatorRunMTU.
func newRecoveryRunMTU(t *testing.T, synDelay time.Duration, udpMTU uint16) *recoveryRun {
	t.Helper()
	r, _, _ := newInitiatorRunMTU(t, udpMTU)
	rr := &recoveryRun{
		initiatorRun: r, t: t,
		libutpSent: map[uint16]bool{}, oursSent: map[uint16]bool{},
		libutpHigh: initiatorConnSeed, oursHigh: initiatorConnSeed,
	}
	rr.advance(synDelay)
	rr.injectBoth(initiatorSynAck())
	if rr.waitForStream() == nil {
		t.Fatal("our side never completed the handshake")
	}
	return rr
}

// sentPacket is one data packet a side emitted.
type sentData struct {
	seq    uint16
	resend bool
}

// data decodes emissions and returns the data packets, marking each as a
// first transmission or a resend.
func (rr *recoveryRun) data(raws [][]byte, sent map[uint16]bool, high *uint16) []sentData {
	rr.t.Helper()
	var out []sentData
	for _, raw := range raws {
		p, err := DecodePacket(raw)
		if err != nil {
			rr.t.Fatalf("emitted packet does not decode: %v", err)
		}
		if p.Header.PacketType != st_data {
			continue
		}
		s := p.Header.SeqNum
		out = append(out, sentData{seq: s, resend: sent[s]})
		sent[s] = true
		if int16(s-*high) > 0 {
			*high = s
		}
	}
	return out
}

func (rr *recoveryRun) takeLibutp() []sentData {
	rr.drv.IssueAcks()
	out := rr.drv.Emitted()
	rr.drv.ClearEmitted()
	return rr.data(out, rr.libutpSent, &rr.libutpHigh)
}

func (rr *recoveryRun) takeOurs() []sentData {
	rr.clk.AwaitQuiet()
	return rr.data(rr.conn.takeEmitted(), rr.oursSent, &rr.oursHigh)
}

// advance moves both clocks by the same amount, giving libutp its timeout
// pass as an embedder would.
func (rr *recoveryRun) advance(d time.Duration) {
	if d <= 0 {
		return
	}
	rr.drv.Advance(uint64(d.Microseconds()))
	rr.drv.CheckTimeouts()
	rr.clk.Advance(d)
}

func (rr *recoveryRun) injectBoth(raw []byte) {
	rr.drv.Inject(raw)
	rr.clk.AwaitReactionTo(func() { rr.conn.inject(raw) })
}

func (rr *recoveryRun) injectLibutp(raw []byte) { rr.drv.Inject(raw) }
func (rr *recoveryRun) injectOurs(raw []byte) {
	rr.clk.AwaitReactionTo(func() { rr.conn.inject(raw) })
}

// writeBoth hands the same bytes to both. Ours is written from a goroutine:
// a large write blocks until it is buffered, and the writer is not a clock
// participant.
func (rr *recoveryRun) writeBoth(b []byte) {
	if _, err := rr.drv.Write(b); err != nil {
		rr.t.Fatalf("libutp write: %v", err)
	}
	s := rr.waitForStream()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.Write(ctx, b)
	}()
	// Let the write reach the connection before anything else happens.
	time.Sleep(20 * time.Millisecond)
	rr.clk.AwaitQuiet()
}

// ackFor is the peer acknowledging everything up to and including seq, with
// an optional selective ack. The receive timestamp difference is small and
// constant, so neither side reads any queueing delay into it.
func ackFor(tsMicros uint32, seq uint16, sack []bool) []byte {
	b := NewPacketBuilder(st_state, initiatorConnSeed, tsMicros, corpusWindow, initiatorFirstInOrder).
		WithAckNum(seq).WithTsDiffMicros(1000)
	if sack != nil {
		b = b.WithSelectiveAck(NewSelectiveAck(sack))
	}
	return b.Build().Encode()
}

// untilResend advances both clocks in steps until each side resends the given
// sequence number, and returns how long that took on each.
func (rr *recoveryRun) untilResend(seq uint16, limit, step time.Duration) (libutpAt, oursAt time.Duration) {
	libutpAt, oursAt = -1, -1
	for elapsed := time.Duration(0); elapsed < limit && (libutpAt < 0 || oursAt < 0); {
		rr.advance(step)
		elapsed += step
		for _, d := range rr.takeLibutp() {
			if d.seq == seq && d.resend && libutpAt < 0 {
				libutpAt = elapsed
			}
		}
		for _, d := range rr.takeOurs() {
			if d.seq == seq && d.resend && oursAt < 0 {
				oursAt = elapsed
			}
		}
	}
	return
}

// libutpRTO measures libutp's retransmission timeout after a chain of round
// trips, to within step.
//
// libutp acts on timeouts only every TIMEOUT_CHECK_INTERVAL, 500ms
// (utp_internal.cpp:37, :3284), so the moment it resends lies up to 500ms
// after its deadline, not on it. The deadline itself is recovered by moving
// the phase of those checks: the last check before the write is placed delta
// earlier, for every delta across the interval, and the shortest time from
// write to resend is the timeout. Each run is microseconds of real time.
func libutpRTO(t *testing.T, samples []time.Duration, step time.Duration) time.Duration {
	t.Helper()
	best := time.Duration(-1)
	for delta := time.Duration(0); delta < 500*time.Millisecond; delta += step {
		drv, err := libutp.NewDriver(1_000_000)
		if err != nil {
			t.Skipf("libutp driver unavailable: %v", err)
		}
		drv.PushRandom(uint32(initiatorConnSeed))
		if err := drv.Connect(); err != nil {
			t.Fatalf("libutp connect: %v", err)
		}
		take := func() []sentData {
			drv.IssueAcks()
			var out []sentData
			for _, raw := range drv.Emitted() {
				if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_data {
					out = append(out, sentData{seq: p.Header.SeqNum})
				}
			}
			drv.ClearEmitted()
			return out
		}
		// No timeout pass while the samples are taken: every wait is shorter
		// than the timeout in force, so none would fire, and passes here
		// would only move the phase this is trying to control.
		drv.Advance(uint64(samples[0].Microseconds()))
		drv.Inject(initiatorSynAck())
		take()
		ts := uint32(300000)
		for i, d := range samples[1:] {
			if _, err := drv.Write([]byte(fmt.Sprintf("sample %d", i))); err != nil {
				t.Fatal(err)
			}
			sent := take()
			if len(sent) != 1 {
				t.Fatalf("libutp sample %d: sent %d packets", i, len(sent))
			}
			drv.Advance(uint64(d.Microseconds()))
			ts += 10000
			drv.Inject(ackFor(ts, sent[0].seq, nil))
			take()
		}
		drv.Advance(uint64((600 * time.Millisecond).Microseconds()))
		drv.CheckTimeouts() // the pass that fixes the phase
		drv.Advance(uint64(delta.Microseconds()))
		if _, err := drv.Write([]byte("left unacknowledged")); err != nil {
			t.Fatal(err)
		}
		sent := take()
		if len(sent) != 1 {
			t.Fatalf("libutp final write: sent %d packets", len(sent))
		}
		for elapsed := time.Duration(0); elapsed < 10*time.Second; {
			drv.Advance(uint64(time.Millisecond.Microseconds()))
			elapsed += time.Millisecond
			drv.CheckTimeouts()
			if again := take(); len(again) > 0 && again[0].seq == sent[0].seq {
				if best < 0 || elapsed < best {
					best = elapsed
				}
				break
			}
		}
		drv.Close()
	}
	return best
}

// oursRTO measures ours after the same chain. Our retransmission wheel arms
// to whole 25ms ticks, so what is measured is the timeout rounded up to one.
func oursRTO(t *testing.T, samples []time.Duration) time.Duration {
	t.Helper()
	rr := newRecoveryRun(t, samples[0])
	defer rr.close()
	rr.takeLibutp()
	rr.takeOurs()
	ts := uint32(300000)
	for i, d := range samples[1:] {
		rr.writeBoth([]byte(fmt.Sprintf("sample %d", i)))
		rr.takeLibutp()
		o := rr.takeOurs()
		if len(o) != 1 {
			t.Fatalf("our sample %d: sent %v", i, o)
		}
		rr.advance(d)
		ts += 10000
		rr.injectOurs(ackFor(ts, o[0].seq, nil))
		rr.takeOurs()
	}
	rr.writeBoth([]byte("left unacknowledged"))
	rr.takeLibutp()
	o := rr.takeOurs()
	if len(o) != 1 {
		t.Fatalf("our final write: sent %v", o)
	}
	_, oursAt := rr.untilResend(o[0].seq, 10*time.Second, time.Millisecond)
	return oursAt
}

// The retransmission timeout is computed as libutp computes it.
//
// libutp keeps rtt and rtt_var in milliseconds, takes the first sample as
// rtt with half of it as rtt_var, smooths later ones by 1/8 and 1/4, and sets
// rto = max(rtt + rtt_var*4, 1000) (utp_internal.cpp:1362-1380). Its
// first-sample test is `rtt == 0`, so a zero first sample leaves the next one
// to be taken as first again. The SYN counts: the SYN-ACK acknowledges it
// through the same ack_packet.
//
// Each case feeds both sides the same chain of round trips -- the SYN-ACK
// after the first, then one data packet acknowledged after each of the rest,
// every wait shorter than the timeout in force -- then leaves one packet
// unacknowledged. libutp's timeout is measured to within 10ms (libutpRTO);
// ours is measured to its wheel's 25ms tick. The tolerance is the sum, and
// ours may not be earlier than libutp's by more than the 10ms.
func TestConformanceRetransmissionTimeoutComputation(t *testing.T) {
	const step = 10 * time.Millisecond
	cases := []struct {
		name    string
		samples []time.Duration // the SYN's round trip first
	}{
		{"floor: SYN answered at once", []time.Duration{0}},
		{"first sample from the SYN", []time.Duration{800 * time.Millisecond}},
		{"a zero SYN sample leaves the next as first", []time.Duration{0, 800 * time.Millisecond}},
		{"smoothing across samples", []time.Duration{500 * time.Millisecond, 300 * time.Millisecond, 1200 * time.Millisecond, 200 * time.Millisecond}},
		{"a jump from a small round trip", []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 900 * time.Millisecond}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theirs := libutpRTO(t, tc.samples, step)
			ours := oursRTO(t, tc.samples)
			t.Logf("timeout: libutp %v (to %v), ours %v (to the %v wheel tick)",
				theirs, step, ours, defaultRetransmitTickInterval)
			if theirs < 0 || ours < 0 {
				t.Fatalf("no resend within 10s: libutp %v, ours %v", theirs, ours)
			}
			if diff := ours - theirs; diff < -step || diff > defaultRetransmitTickInterval+step {
				t.Errorf("ours %v against libutp's %v: outside one wheel tick", ours, theirs)
			}
		})
	}
}

// The zero-window probe fires when libutp's does -- where libutp's fires at all.
//
// An acknowledgement advertising a zero window arms a timer, `zerowindow_time
// = current_ms + 15000` (utp_internal.cpp:2149-2151). When a timeout pass
// finds it expired with the window still zero, libutp opens the window to one
// PACKET_SIZE (:1142-1145), and the next flush sends a packet.
//
// PACKET_SIZE is 1435 (:57). The flush charges a whole get_packet_size()
// against it (is_full with no argument, :933-936, :974), and that is the MTU
// the embedder reports less the 20-byte header. With libutp's own default MTU
// callback, 1402 on IPv4 (utp_utils.cpp:228), a packet is 1382 bytes and fits.
// With an embedder reporting a real Ethernet interface, 1472, a packet is 1452
// bytes and never fits: libutp's probe never fires, and a connection whose
// peer advertised a zero window and then went quiet stays stalled until it
// dies. Ours opens the window to MaxPacketSize, which the payload never
// exceeds, so it probes at any MTU.
//
// Both are asserted, so a change on either side shows. libutp's probe waits
// for a timeout pass, which it runs only every 500ms (:37, :3284); ours fires
// on the interval exactly.
func TestConformanceZeroWindowProbeTiming(t *testing.T) {
	t.Run("libutp's default MTU, 1402", func(t *testing.T) {
		libutpAt, oursAt := zeroWindowProbeTimes(t, 1402)
		if libutpAt < 0 || oursAt < 0 {
			t.Fatalf("no probe within 40s: libutp %v, ours %v", libutpAt, oursAt)
		}
		if oursAt != 15*time.Second {
			t.Errorf("ours probed after %v; libutp's interval is 15s", oursAt)
		}
		if d := libutpAt - oursAt; d < 0 || d > 500*time.Millisecond {
			t.Errorf("libutp probed after %v, ours after %v; libutp may be up to one "+
				"500ms timeout pass later, never earlier", libutpAt, oursAt)
		}
	})
	t.Run("an Ethernet interface's MTU, 1472", func(t *testing.T) {
		libutpAt, oursAt := zeroWindowProbeTimes(t, 1472)
		if oursAt != 15*time.Second {
			t.Errorf("ours probed after %v; libutp's interval is 15s", oursAt)
		}
		if libutpAt >= 0 {
			t.Errorf("libutp probed after %v. At this MTU its 1452-byte packet does not "+
				"fit the 1435-byte PACKET_SIZE its probe opens, and it never has; if it "+
				"now does, the vendored libutp has changed and DEVIATIONS.md should say so",
				libutpAt)
		}
	})
}

// zeroWindowProbeTimes advertises a zero window to both sides, writes, and
// returns how long each took to send anything; -1 for nothing within 40s.
func zeroWindowProbeTimes(t *testing.T, mtu uint16) (libutpAt, oursAt time.Duration) {
	t.Helper()
	rr := newRecoveryRunMTU(t, 0, mtu)
	defer rr.close()
	rr.takeLibutp()
	rr.takeOurs()

	rr.injectBoth(initiatorState(200000, 0))
	rr.writeBoth([]byte("stalled behind a zero window"))
	if l, o := rr.takeLibutp(), rr.takeOurs(); len(l) != 0 || len(o) != 0 {
		t.Fatalf("sent into a zero window: libutp %v, ours %v", l, o)
	}

	const step = 10 * time.Millisecond
	libutpAt, oursAt = -1, -1
	for elapsed := time.Duration(0); elapsed < 40*time.Second && (libutpAt < 0 || oursAt < 0); {
		rr.advance(step)
		elapsed += step
		if l := rr.takeLibutp(); len(l) > 0 && libutpAt < 0 {
			libutpAt = elapsed
		}
		if o := rr.takeOurs(); len(o) > 0 && oursAt < 0 {
			oursAt = elapsed
		}
	}
	t.Logf("MTU %d: first packet through the zero window: libutp %v, ours %v", mtu, libutpAt, oursAt)
	return libutpAt, oursAt
}

// grown is the state after growWindows: each side's oldest outstanding
// sequence number and how many it has in flight.
type grown struct {
	libutpOldest, oursOldest uint16
	libutpCount, oursCount   int
	ts                       uint32
}

// growWindows writes a large buffer to both sides and acknowledges everything
// each sends, a round trip at a time, until both have at least want packets
// in flight. libutp's window opens at one packet and grows by about one a
// round trip in slow start (utp_internal.cpp:1691-1702); ours opens at two
// and grows faster, so ours will have more in flight. That does not matter:
// each scenario is expressed relative to each side's own oldest packet.
func (rr *recoveryRun) growWindows(want int) grown {
	rr.t.Helper()
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	rr.writeBoth(payload)
	libutpAcked, oursAcked := uint16(initiatorConnSeed), uint16(initiatorConnSeed)
	ts := uint32(300000)
	for round := 0; round < 80; round++ {
		rr.takeLibutp()
		rr.takeOurs()
		ln, on := int(rr.libutpHigh-libutpAcked), int(rr.oursHigh-oursAcked)
		if ln >= want && on >= want {
			return grown{libutpOldest: libutpAcked + 1, oursOldest: oursAcked + 1,
				libutpCount: ln, oursCount: on, ts: ts}
		}
		rr.advance(50 * time.Millisecond)
		ts += 50000
		if ln < want {
			libutpAcked = rr.libutpHigh
			rr.injectLibutp(ackFor(ts, libutpAcked, nil))
		}
		if on < want {
			oursAcked = rr.oursHigh
			rr.injectOurs(ackFor(ts, oursAcked, nil))
		}
	}
	rr.t.Fatalf("windows did not grow to %d in flight: libutp %d, ours %d", want,
		int(rr.libutpHigh-libutpAcked), int(rr.oursHigh-oursAcked))
	return grown{}
}

// sackFrom builds a selective ack for a peer that has everything before
// oldest and, past it, the packets at the given offsets from oldest. Bit i of
// the extension is ack_nr+2+i, which is oldest+1+i: oldest itself is the one
// the cumulative ack is waiting for and cannot appear in it.
func sackFrom(offsets []int) []bool {
	bits := make([]bool, 32)
	for _, o := range offsets {
		bits[o-1] = true
	}
	return bits
}

// resentOffsets is which packets a side resent, as offsets from its oldest.
func resentOffsets(sent []sentData, oldest uint16) []int {
	var out []int
	for _, d := range sent {
		if d.resend {
			out = append(out, int(d.seq-oldest))
		}
	}
	return out
}

// Fast retransmission decides what libutp decides.
//
// libutp's selective_ack walks the bits from the top down, counting packets
// acknowledged past each one; an unacknowledged packet with at least
// DUPLICATE_ACKS_BEFORE_RESEND (3) past it, and not below fast_resend_seq_nr,
// is resent, and so is the packet the cumulative ack is waiting for, on the
// same count. At most four per acknowledgement, oldest first, each moving
// fast_resend_seq_nr past it so it is not fast resent again
// (utp_internal.cpp:1480-1610). A duplicate acknowledgement with no selective
// ack resends nothing: libutp's duplicate count feeds only its MTU search
// (:1921-1941).
//
// Each scenario starts from a fresh connection with at least 14 packets in
// flight on both sides, and compares which packets each resent, as offsets
// from its own oldest outstanding one.
func TestConformanceFastRetransmitDecisions(t *testing.T) {
	type ack struct {
		sacked []int // offsets past the oldest; nil for a bare duplicate ack
	}
	cases := []struct {
		name string
		acks []ack
		want [][]int // per acknowledgement, what libutp's source says it resends
	}{
		{"two acknowledged past a gap: below the threshold",
			[]ack{{[]int{2, 3}}}, [][]int{nil}},
		{"three acknowledged past a gap: the gap and the oldest",
			[]ack{{[]int{2, 3, 4}}}, [][]int{{0, 1}}},
		{"a gap of six: four resent, oldest first",
			[]ack{{[]int{6, 7, 8, 9, 10, 11, 12, 13}}}, [][]int{{0, 1, 2, 3}}},
		{"the next acknowledgement: not the same four again",
			[]ack{{[]int{6, 7, 8, 9, 10, 11, 12, 13}}, {[]int{6, 7, 8, 9, 10, 11, 12, 13}}},
			[][]int{{0, 1, 2, 3}, {4, 5}}},
		{"three bare duplicate acknowledgements: nothing",
			[]ack{{nil}, {nil}, {nil}}, [][]int{nil, nil, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := newRecoveryRun(t, 0)
			defer rr.close()
			rr.takeLibutp()
			rr.takeOurs()
			g := rr.growWindows(14)
			t.Logf("in flight: libutp %d from %d, ours %d from %d",
				g.libutpCount, g.libutpOldest, g.oursCount, g.oursOldest)
			ts := g.ts
			for i, a := range tc.acks {
				ts += 1000
				var bits []bool
				if a.sacked != nil {
					bits = sackFrom(a.sacked)
				}
				rr.injectLibutp(ackFor(ts, g.libutpOldest-1, bits))
				rr.injectOurs(ackFor(ts, g.oursOldest-1, bits))
				l := resentOffsets(rr.takeLibutp(), g.libutpOldest)
				o := resentOffsets(rr.takeOurs(), g.oursOldest)
				t.Logf("ack %d: libutp resent %v, ours %v", i, l, o)
				if fmt.Sprint(l) != fmt.Sprint(tc.want[i]) {
					t.Errorf("ack %d: libutp resent %v; its source reads as %v -- the "+
						"harness or the reading is wrong", i, l, tc.want[i])
				}
				if fmt.Sprint(o) != fmt.Sprint(l) {
					t.Errorf("ack %d: ours resent %v, libutp %v", i, o, l)
				}
			}
		})
	}
}

// ccEntry is one line of libutp's congestion-control log: the inputs
// apply_ccontrol acted on and the window that resulted
// (utp_internal.cpp:1713-1730).
type ccEntry struct {
	raw        string
	ourDelayMs int64
	targetMs   int64
	acked      uint32
	maxWindow  uint32
	packetSize uint32
	lastMaxed  int64
	nowMs      int64
	sndbuf     uint32
	penaltyMs  int64
	gain       float64
}

var ccField = regexp.MustCompile(`([a-z_]+):(-?[0-9.]+)`)

func parseCCLog(t *testing.T, lines []string) []ccEntry {
	t.Helper()
	var out []ccEntry
	for _, l := range lines {
		f := map[string]string{}
		for _, m := range ccField.FindAllStringSubmatch(l, -1) {
			f[m[1]] = m[2]
		}
		num := func(k string) int64 {
			v, err := strconv.ParseInt(f[k], 10, 64)
			if err != nil {
				t.Fatalf("libutp cc log: field %q in %q: %v", k, l, err)
			}
			return v
		}
		g, err := strconv.ParseFloat(f["scaled_gain"], 64)
		if err != nil {
			t.Fatalf("libutp cc log: scaled_gain in %q: %v", l, err)
		}
		out = append(out, ccEntry{
			raw: l, ourDelayMs: num("our_delay"), targetMs: num("target_delay"),
			acked: uint32(num("acked_bytes")), maxWindow: uint32(num("max_window")),
			packetSize: uint32(num("packet_size")), lastMaxed: num("last_maxed_out_window"),
			nowMs: num("current_ms"), sndbuf: uint32(num("opt_sndbuf")),
			penaltyMs: num("delay_penalty"), gain: g,
		})
	}
	return out
}

// ccPhase is a stretch of acknowledgements with one queueing delay.
type ccPhase struct {
	name    string
	acks    int
	delayMs uint32        // queueing delay the peer reports, over a fixed base
	gap     time.Duration // between acknowledgements
	// appLimited writes 100 bytes before each acknowledgement instead of
	// keeping a large write queued, so the window is never full.
	appLimited bool
}

// libutpCCTrace drives libutp alone through the phases, acknowledging its
// oldest outstanding packet one at a time with the given delays, and returns
// its congestion-control log. initialWrite bytes are written before the first
// phase.
//
// Delays are whole milliseconds over a whole-millisecond base, and the clock
// moves in whole milliseconds, so every delay libutp acts on is a whole
// number of milliseconds -- which the log prints exactly.
func libutpCCTrace(t *testing.T, initialWrite int, phases []ccPhase) []ccEntry {
	t.Helper()
	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()
	drv.EnableCCLog()
	drv.PushRandom(uint32(initiatorConnSeed))
	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	drv.Inject(initiatorSynAck())
	drv.IssueAcks()

	var outstanding []uint16
	sent := map[uint16]bool{}
	take := func() {
		drv.IssueAcks()
		for _, raw := range drv.Emitted() {
			p, err := DecodePacket(raw)
			if err != nil || p.Header.PacketType != st_data || sent[p.Header.SeqNum] {
				continue
			}
			sent[p.Header.SeqNum] = true
			outstanding = append(outstanding, p.Header.SeqNum)
		}
		drv.ClearEmitted()
	}
	if initialWrite > 0 {
		if _, err := drv.Write(make([]byte, initialWrite)); err != nil {
			t.Fatal(err)
		}
	}
	take()
	const baseMicros = 20000
	ts := uint32(300000)
	for _, ph := range phases {
		for i := 0; i < ph.acks; i++ {
			if ph.appLimited {
				if _, err := drv.Write(make([]byte, 100)); err != nil {
					t.Fatal(err)
				}
			}
			take()
			if len(outstanding) == 0 {
				t.Fatalf("phase %q ack %d: nothing outstanding to acknowledge", ph.name, i)
			}
			drv.Advance(uint64(ph.gap.Microseconds()))
			drv.CheckTimeouts()
			ts += uint32(ph.gap.Microseconds())
			seq := outstanding[0]
			outstanding = outstanding[1:]
			drv.Inject(NewPacketBuilder(st_state, initiatorConnSeed, ts, corpusWindow, initiatorFirstInOrder).
				WithAckNum(seq).WithTsDiffMicros(baseMicros + ph.delayMs*1000).Build().Encode())
			take()
		}
	}
	return parseCCLog(t, drv.CCLog())
}

// ccReplay is what replaying a libutp trace into our controller found.
type ccReplay struct {
	mismatches []string
	// slowStartEnd is the entry at which our controller, fed libutp's
	// inputs, left slow start, and -1 if it never did.
	slowStartEnd int
}

// replayCC feeds libutp's logged inputs, one acknowledgement at a time, to our
// classic-LEDBAT update, and records every entry where the window we compute
// differs from the window libutp logged.
//
// Our controller starts, at entry `from`, from libutp's state: the window
// libutp had before that entry (its first window is one packet_size), its
// target, its opt_sndbuf as both ssthresh and ceiling, and slow start on
// (utp_internal.cpp:2567, :2620-2621). From there it evolves by its own rules;
// only the inputs come from libutp. The delay is fed as the delay libutp acted
// on, after its clamp to the round trip, with no clamp of our own;
// last_maxed_out_window and the clock come from the log, 0 included.
//
// One documented deviation has to be neutralised for the rules to be
// compared at all. Our slow-start step is half our window floor, and libutp's
// is one current packet_size; setting our floor to two of libutp's packets
// makes the steps equal. The floor itself then differs from libutp's 10 bytes
// (DEVIATIONS.md), so the replay fails the case if libutp's window is ever
// below it, where the floor rather than the rules would decide.
func replayCC(t *testing.T, entries []ccEntry, from int) ccReplay {
	t.Helper()
	if len(entries) <= from {
		t.Fatalf("libutp logged %d congestion-control updates; the replay starts at %d", len(entries), from)
	}
	c := newDefaultController(fromConnConfig(NewConnectionConfig()))
	first := entries[from]
	c.targetDelayMicros = uint32(first.targetMs * 1000)
	c.minWindowSizeBytes = 2 * first.packetSize
	c.maxWindowSizeBytes = first.packetSize
	if from > 0 {
		c.maxWindowSizeBytes = entries[from-1].maxWindow
	}
	c.ssthreshBytes = first.sndbuf
	c.maxWindowUpperBytes = first.sndbuf
	c.slowStart = true
	r := ccReplay{slowStartEnd: -1}
	for i := from; i < len(entries); i++ {
		e := entries[i]
		if e.penaltyMs != 0 {
			t.Fatalf("entry %d: libutp applied a drift penalty; this replay assumes none", i)
		}
		if e.packetSize*2 != c.minWindowSizeBytes {
			t.Fatalf("entry %d: packet_size changed from %d to %d mid-trace", i, first.packetSize, e.packetSize)
		}
		if e.maxWindow < c.minWindowSizeBytes || (i > from && entries[i-1].maxWindow < c.minWindowSizeBytes) {
			t.Fatalf("entry %d: libutp's window %d is below our floor %d; the floor would "+
				"decide this comparison, not the rules", i, e.maxWindow, c.minWindowSizeBytes)
		}
		if e.lastMaxed == 0 {
			c.lastMaxedOutWindow = time.Time{}
		} else {
			c.lastMaxedOutWindow = time.UnixMilli(e.lastMaxed)
		}
		before := c.maxWindowSizeBytes
		wasSlow := c.slowStart
		c.applyCongestionControl(0, uint32(e.ourDelayMs*1000), e.acked, time.Hour, time.UnixMilli(e.nowMs))
		if wasSlow && !c.slowStart {
			r.slowStartEnd = i
		}
		if c.maxWindowSizeBytes != e.maxWindow {
			r.mismatches = append(r.mismatches, fmt.Sprintf(
				"entry %d: from %d, delay %dms, acked %d, last full at %dms, now %dms: libutp -> %d (gain %.1f), ours -> %d",
				i, before, e.ourDelayMs, e.acked, e.lastMaxed, e.nowMs, e.maxWindow, e.gain, c.maxWindowSizeBytes))
			// Carry on from libutp's window, so one difference does not
			// show up as every later entry differing.
			c.maxWindowSizeBytes = e.maxWindow
		}
	}
	return r
}

// LEDBAT's window adjustment, rule by rule, against libutp.
//
// COMPATIBILITY.md rated these rules "partly measured": the controller's
// overall response to a queue was compared against libutp's, but each rule
// in apply_ccontrol (utp_internal.cpp:1615-1712) was only read. Here libutp
// is driven through phases that each exercise a rule, and every update it
// makes is replayed into ours (replayCC). A delay registers with libutp only
// once three acknowledgements carry it, since it acts on the least of its
// last three samples (CUR_DELAY_SIZE, :75), so each phase is longer than
// that. The first phase carries no queueing delay, so libutp's base delay is
// the fixed one and each later phase's delay is the queueing delay it names.
// The round trip must exceed that delay or libutp's clamp to the minimum
// round trip (:1617-1621) decides it instead; with the window full the queue
// of unacknowledged packets makes the round trip long, and with it never
// full the gap between acknowledgements is the round trip.
func TestConformanceLedbatRules(t *testing.T) {
	t.Run("window full: slow start, delay exit, increase, decrease", func(t *testing.T) {
		entries := libutpCCTrace(t, 4<<20, []ccPhase{
			{name: "slow start", acks: 25, delayMs: 0, gap: 30 * time.Millisecond},
			{name: "slow start exits on delay over 0.9 target", acks: 6, delayMs: 95, gap: 30 * time.Millisecond},
			{name: "increase below target", acks: 30, delayMs: 40, gap: 30 * time.Millisecond},
			{name: "decrease above target", acks: 12, delayMs: 160, gap: 30 * time.Millisecond},
			{name: "at target", acks: 6, delayMs: 100, gap: 30 * time.Millisecond},
		})
		r := replayCC(t, entries, 0)
		checkCCCoverage(t, entries, r)
		for _, m := range r.mismatches {
			t.Error(m)
		}
	})

	// The application never fills the window, so libutp's is_full never
	// records a time (utp_internal.cpp:945, :957) and last_maxed_out_window
	// keeps its initial 0 (:2603). Slow start grows the window regardless
	// (:1691-1702 do not look at scaled_gain); after it, the gain is zeroed
	// by the application-limited guard (:1681-1686).
	t.Run("never full: the guard from the start", func(t *testing.T) {
		entries := libutpCCTrace(t, 0, []ccPhase{
			{name: "slow start past our floor", acks: 30, delayMs: 0, gap: 30 * time.Millisecond, appLimited: true},
			{name: "slow start exits on delay", acks: 6, delayMs: 95, gap: 150 * time.Millisecond, appLimited: true},
			{name: "below target, window never full", acks: 20, delayMs: 20, gap: 150 * time.Millisecond, appLimited: true},
		})
		floor := 2 * entries[0].packetSize
		from := -1
		for i := 1; i < len(entries); i++ {
			if entries[i-1].maxWindow >= floor {
				from = i
				break
			}
		}
		if from < 0 {
			t.Fatalf("libutp's window never reached %d; the replay has nothing to start from", floor)
		}
		for i := 0; i < from; i++ {
			if entries[i].ourDelayMs*10 > entries[i].targetMs*9 {
				t.Fatalf("libutp saw a high delay at entry %d, before the replay starts at %d", i, from)
			}
		}
		guarded := 0
		for _, e := range entries[from:] {
			if e.lastMaxed != 0 {
				t.Fatalf("libutp recorded a full window at %dms; this case needs it never full", e.lastMaxed)
			}
			if e.gain == 0 && e.ourDelayMs < e.targetMs {
				guarded++
			}
		}
		r := replayCC(t, entries, from)
		t.Logf("%d updates replayed from entry %d; slow start ended at %d; %d below target with the gain zeroed",
			len(entries)-from, from, r.slowStartEnd, guarded)
		if r.slowStartEnd < 0 || guarded == 0 {
			t.Fatalf("the trace did not leave slow start and reach the guard")
		}
		for _, m := range r.mismatches {
			t.Error(m)
		}
	})

	// The window fills, the write drains, and the application then trickles.
	// is_full records the time the window was last full (:957); a second
	// later the guard zeroes the gain (:1681).
	t.Run("full, then application-limited for over a second", func(t *testing.T) {
		entries := libutpCCTrace(t, 40000, []ccPhase{
			{name: "slow start with the window full", acks: 20, delayMs: 0, gap: 30 * time.Millisecond},
			{name: "the write drains, then a trickle", acks: 25, delayMs: 20, gap: 150 * time.Millisecond, appLimited: true},
		})
		// libutp's window starts at one packet, below our floor: start where
		// it has passed it.
		floor := 2 * entries[0].packetSize
		from := -1
		for i := 1; i < len(entries); i++ {
			if entries[i-1].maxWindow >= floor {
				from = i
				break
			}
		}
		if from < 0 {
			t.Fatalf("libutp's window never reached %d", floor)
		}
		var grewWhileRecent, guardedAfterFull int
		for _, e := range entries[from:] {
			if e.lastMaxed == 0 {
				continue
			}
			if e.nowMs-e.lastMaxed > 1000 && e.gain == 0 && e.ourDelayMs < e.targetMs {
				guardedAfterFull++
			}
			if e.nowMs-e.lastMaxed <= 1000 && e.gain > 0 {
				grewWhileRecent++
			}
		}
		r := replayCC(t, entries, from)
		t.Logf("%d updates replayed from entry %d: %d with gain while the window was full within the second, "+
			"%d with the gain zeroed more than a second after it", len(entries)-from, from, grewWhileRecent, guardedAfterFull)
		if grewWhileRecent == 0 || guardedAfterFull == 0 {
			t.Fatalf("the trace did not cross from a recently full window to the guard")
		}
		for _, m := range r.mismatches {
			t.Error(m)
		}
	})
}

// checkCCCoverage fails the case if the trace did not exercise the rules it
// was built for, so a harness change cannot quietly turn it into a
// comparison of nothing.
func checkCCCoverage(t *testing.T, entries []ccEntry, r ccReplay) {
	t.Helper()
	var grewAfterSS, shrank int
	for i := 1; i < len(entries); i++ {
		if entries[i].maxWindow > entries[i-1].maxWindow && r.slowStartEnd >= 0 && i > r.slowStartEnd {
			grewAfterSS++
		}
		if entries[i].maxWindow < entries[i-1].maxWindow {
			shrank++
		}
	}
	t.Logf("%d updates: slow start ended at %d; window grew %d times after it, shrank %d",
		len(entries), r.slowStartEnd, grewAfterSS, shrank)
	if r.slowStartEnd < 0 {
		t.Fatalf("the trace never left slow start")
	}
	if e := entries[r.slowStartEnd]; e.ourDelayMs*10 <= e.targetMs*9 {
		t.Fatalf("slow start ended at entry %d with delay %dms, not on the 0.9-target exit", r.slowStartEnd, e.ourDelayMs)
	}
	if grewAfterSS == 0 || shrank == 0 {
		t.Fatalf("the trace did not exercise both the increase and the decrease")
	}
}
