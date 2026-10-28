//go:build cgo

package utp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/arxodeus/utp/internal/libutp"
)

// Loss recovery, compared with the reference implementation on a virtual
// clock: when each side resends, and which packets.
//
// Transcripts cannot be compared field by field here: the reference window
// opens at one packet and ours at two, so after the first round trip the
// sides send different numbers of packets with different sequence numbers.
// These cases drive each side separately, in lockstep on the clock, and
// compare decisions rather than bytes: how long until a resend, and which
// packet relative to that side's own oldest outstanding one.

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

// newRecoveryRunMTU is newRecoveryRun with the reference driver's reported
// MTU set; see newInitiatorRunMTU.
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

// sentData is one data packet a side emitted.
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
		p, err := decodePacket(raw, false)
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

// advance moves both clocks by the same amount, giving the reference its
// timeout pass as an embedding application would.
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
	b := newPacketBuilder(st_state, initiatorConnSeed, tsMicros, corpusWindow, initiatorFirstInOrder).
		WithAckNum(seq).WithTsDiffMicros(1000)
	if sack != nil {
		b = b.WithSelectiveAck(newSelectiveAck(sack))
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

// libutpRTO measures the reference implementation's retransmission timeout
// after a chain of round trips, to within step.
//
// It acts on timeouts only every 500ms, so it resends up to 500ms after its
// deadline. The deadline is recovered by shifting the phase of those checks
// across the interval: the shortest time from write to resend is the
// timeout.
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
				if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == st_data {
					out = append(out, sentData{seq: p.Header.SeqNum})
				}
			}
			drv.ClearEmitted()
			return out
		}
		// No timeout pass while the samples are taken: every wait is shorter
		// than the timeout in force, so none would fire, and passes would only
		// move the phase being controlled.
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
// to whole 25ms ticks, so the measurement is the timeout rounded up to one.
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

// The retransmission timeout is computed as the reference computes it: rtt
// and rtt_var in milliseconds, the first sample taken as rtt with half of it
// as rtt_var, later ones smoothed by 1/8 and 1/4, and
// rto = max(rtt + rtt_var*4, 1000). A zero first sample leaves the next to be
// taken as first. The SYN's round trip counts.
//
// Each case feeds both sides the same chain of round trips, then leaves one
// packet unacknowledged. The reference timeout is measured to within 10ms
// (libutpRTO) and ours to the wheel's 25ms tick; the tolerance is the sum,
// and ours may not be earlier by more than 10ms.
func TestConformanceRetransmissionTimeoutComputation(t *testing.T) {
	// The loss probe would add one packet before the first timeout; it is
	// tested on its own in loss_probe_test.go.
	withoutLossProbe(t)
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

// The zero-window probe fires when the reference's does, where the
// reference's fires at all.
//
// An acknowledgement advertising a zero window arms a 15-second timer; when
// it expires with the window still zero, the window is opened to one packet
// and the next flush sends one. The reference opens it to a fixed 1435
// bytes, while a packet at an Ethernet MTU of 1472 is 1452 bytes, so at that
// MTU its probe never fires. Ours opens the window to MaxPacketSize, so it
// probes at any MTU. Both are asserted. The reference also waits for a
// timeout pass, which runs every 500ms; ours fires on the interval exactly.
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
// in flight. Ours grows faster and will have more in flight, which does not
// matter: each scenario is expressed relative to each side's own oldest
// packet.
func (rr *recoveryRun) growWindows(want int) grown {
	rr.t.Helper()
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	rr.writeBoth(payload)
	// Ours is written from a goroutine that is not on the virtual clock and
	// may start late on a loaded machine. Rounds spent waiting for it would
	// leave the reference unacknowledged until its timeout fired, comparing a
	// timeout with a fast retransmit, so no virtual time passes until ours
	// has started.
	for deadline := time.Now().Add(10 * time.Second); rr.oursHigh == initiatorConnSeed; {
		if time.Now().After(deadline) {
			rr.t.Fatal("our side sent no data within 10s of the write")
		}
		rr.takeOurs()
		time.Sleep(time.Millisecond)
	}
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

// Fast retransmission decides what the reference decides. Walking the
// selective ack from the top down, an unacknowledged packet with at least 3
// packets acknowledged past it, and not below the fast-retransmit floor, is
// resent, as is the packet the cumulative ack is waiting for on the same
// count. At most four per acknowledgement, oldest first, each moving the
// floor past it. A duplicate acknowledgement without a selective ack resends
// nothing.
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
		want [][]int // per acknowledgement, the packets the reference rules resend
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

// ccEntry is one line of the reference implementation's congestion-control
// log: the inputs it acted on and the resulting window.
type ccEntry struct {
	raw        string
	actualUs   int64 // the delay the acknowledgement reported, unfiltered
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
			raw: l, actualUs: num("actual_delay"), ourDelayMs: num("our_delay"), targetMs: num("target_delay"),
			acked: uint32(num("acked_bytes")), maxWindow: uint32(num("max_window")),
			packetSize: uint32(num("packet_size")), lastMaxed: num("last_maxed_out_window"),
			nowMs: num("current_ms"), sndbuf: uint32(num("opt_sndbuf")),
			penaltyMs: num("delay_penalty"), gain: g,
		})
	}
	return out
}

// ccPhase is a stretch of acknowledgements with one queuing delay.
type ccPhase struct {
	name    string
	acks    int
	delayMs uint32        // queuing delay the peer reports, over a fixed base
	gap     time.Duration // between acknowledgements
	// appLimited writes 100 bytes before each acknowledgement instead of
	// keeping a large write queued, so the window is never full.
	appLimited bool
}

// libutpCCTrace drives the reference implementation alone through the
// phases, acknowledging its oldest outstanding packet one at a time with the
// given delays, and returns its congestion-control log. initialWrite bytes
// are written before the first phase. Delays, base and clock steps are whole
// milliseconds, so the log, which prints milliseconds, is exact.
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
			p, err := decodePacket(raw, false)
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
			drv.Inject(newPacketBuilder(st_state, initiatorConnSeed, ts, corpusWindow, initiatorFirstInOrder).
				WithAckNum(seq).WithTsDiffMicros(baseMicros + ph.delayMs*1000).Build().Encode())
			take()
		}
	}
	return parseCCLog(t, drv.CCLog())
}

// ccReplay is what replaying a reference trace into our controller found.
type ccReplay struct {
	mismatches []string
	// slowStartEnd is the entry at which our controller, fed the reference's
	// inputs, left slow start, or -1 if it never did.
	slowStartEnd int
}

// replayCC feeds the reference's logged inputs, one acknowledgement at a
// time, to our classic LEDBAT update, and records every entry where our
// window differs from the logged one.
//
// At entry from, our controller starts from the reference's state: its
// window before that entry, its target, its window ceiling as both ssthresh
// and ceiling, and slow start on. From there it follows its own rules; only
// the inputs come from the log. The delay fed is the one the reference acted
// on, after its clamp to the round trip.
//
// Our slow-start step is half our window floor, the reference's one packet,
// so the floor is set to two of the reference's packets to make the steps
// equal. The replay fails the case if the reference's window ever falls
// below that floor, where the floor rather than the rules would decide.
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
		c.applyCongestionControl(0, uint32(e.ourDelayMs*1000), e.acked, time.Hour, time.UnixMilli(e.nowMs), 0)
		if wasSlow && !c.slowStart {
			r.slowStartEnd = i
		}
		if c.maxWindowSizeBytes != e.maxWindow {
			r.mismatches = append(r.mismatches, fmt.Sprintf(
				"entry %d: from %d, delay %dms, acked %d, last full at %dms, now %dms: libutp -> %d (gain %.1f), ours -> %d",
				i, before, e.ourDelayMs, e.acked, e.lastMaxed, e.nowMs, e.maxWindow, e.gain, c.maxWindowSizeBytes))
			// Continue from the reference's window, so one difference does not show
			// up as every later entry differing.
			c.maxWindowSizeBytes = e.maxWindow
		}
	}
	return r
}

// LEDBAT's window adjustment, rule by rule, against the reference. It is
// driven through phases that each exercise a rule, and every update it makes
// is replayed into ours (replayCC).
//
// A delay registers only once three acknowledgements carry it, since the
// least of the last three samples is used, so each phase is longer than
// that. The first phase carries no queuing delay, so the base delay is the
// fixed one and each later phase's delay is the queuing delay it names. The
// round trip must exceed that delay or the clamp to the minimum round trip
// decides instead; with the window full the queue of unacknowledged packets
// makes the round trip long, and with it never full the gap between
// acknowledgements is the round trip.
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

	// The application never fills the window, so the time it was last full
	// is never recorded and stays 0. Slow start grows the window regardless;
	// after it, the application-limited guard zeroes the gain.
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

	// The window fills, the write drains, and the application then
	// trickles. A second after the window was last full, the guard zeroes
	// the gain.
	t.Run("full, then application-limited for over a second", func(t *testing.T) {
		entries := libutpCCTrace(t, 40000, []ccPhase{
			{name: "slow start with the window full", acks: 20, delayMs: 0, gap: 30 * time.Millisecond},
			{name: "the write drains, then a trickle", acks: 25, delayMs: 20, gap: 150 * time.Millisecond, appLimited: true},
		})
		// The reference window starts at one packet, below our floor: start
		// once it has passed it.
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
// was built for, so a harness change cannot turn it into a comparison of
// nothing.
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
