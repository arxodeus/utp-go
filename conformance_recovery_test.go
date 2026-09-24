//go:build cgo

package utp_go

import (
	"context"
	"fmt"
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
