package utp_go

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rackHarness drives a send window through the real acknowledgement path on
// a clock the test sets.
type rackHarness struct {
	t   *testing.T
	sp  *sentPackets
	t0  time.Time
	seq []uint16 // seq[i] is packet i's sequence number, from 1
}

func newRackHarness(t *testing.T, rack bool) *rackHarness {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	sp := newSentPacketsWithoutLogger(1000, ctrl)
	sp.rack.active = rack
	return &rackHarness{t: t, sp: sp, t0: time.Unix(1_700_000_000, 0), seq: []uint16{1000}}
}

func (h *rackHarness) at(ms int) time.Time { return h.t0.Add(time.Duration(ms) * time.Millisecond) }

// send sends the next packet at ms and returns its index.
func (h *rackHarness) send(ms int) int {
	seq := h.sp.NextSeqNum()
	h.sp.OnTransmit(seq, st_data, []byte{0}, 1, h.at(ms))
	h.seq = append(h.seq, seq)
	return len(h.seq) - 1
}

// resend sends packet i again at ms.
func (h *rackHarness) resend(i, ms int) {
	h.sp.OnTransmit(h.seq[i], st_data, []byte{0}, 1, h.at(ms))
}

// ack acknowledges cumulatively through packet upTo (0 for none) and
// selectively the packets in sacked, at ms.
func (h *rackHarness) ack(ms, upTo int, sacked ...int) {
	h.t.Helper()
	ackNum := h.seq[upTo]
	var sack *SelectiveAck
	if len(sacked) > 0 {
		bits := make([]bool, 32)
		for _, i := range sacked {
			bits[int(h.seq[i]-ackNum-2)] = true
		}
		sack = NewSelectiveAck(bits)
	}
	_, _, err := h.sp.onAck(ackNum, sack, 0, h.at(ms))
	require.NoError(h.t, err)
}

// lost is the indexes of the packets queued for resending now.
func (h *rackHarness) lost() []int {
	var out []int
	for _, p := range h.sp.TakeLostPackets() {
		for i, s := range h.seq {
			if i > 0 && s == p.SeqNum {
				out = append(out, i)
			}
		}
	}
	return out
}

// Once the path has been seen to reorder, three packets acknowledged past a
// hole are not enough: a packet is lost only once a round trip and a quarter
// of the minimum round trip have passed since it was sent. libutp's count
// declares it lost at once.
func TestRackReorderingWithinWindowIsNotLoss(t *testing.T) {
	scenario := func(h *rackHarness) {
		h.send(0)       // 1
		h.ack(20, 1)    // a 20 ms round trip
		h.send(20)      // 2
		h.send(20)      // 3
		h.ack(40, 1, 3) // 3 overtakes 2...
		h.ack(41, 3)    // ...which arrives: the path reorders
		for ms := 41; ms <= 45; ms++ {
			h.send(ms) // 4..8
		}
		h.ack(62, 3, 6, 7, 8) // 6, 7 and 8 past a hole at 4 and 5
	}

	off := newRackHarness(t, false)
	scenario(off)
	require.Equal(t, []int{4, 5}, off.lost(), "libutp's rule: three past the hole is loss")

	on := newRackHarness(t, true)
	scenario(on)
	require.True(t, on.sp.rack.reorderingSeen)
	require.Empty(t, on.lost(), "inside the reordering window, nothing is lost yet")
	// The minimum round trip is now 17 ms (packet 8's), so the window is a
	// quarter of it. Packet 4, sent at 41 ms, passes it at 41 + 17 (the
	// round trip to packet 8) + 4.25 = 62.25 ms; packet 5 at 63.25.
	require.Equal(t, 1250*time.Microsecond, on.sp.rack.timeout)

	on.sp.rackDetectAndMark(on.at(63))
	require.Equal(t, []int{4}, on.lost())
	on.sp.rackDetectAndMark(on.at(64))
	require.Equal(t, []int{5}, on.lost())
}

// Before the path has been seen to reorder, three packets past a hole make
// it loss at once, as libutp's count does.
func TestRackWithoutReorderingMatchesTheCount(t *testing.T) {
	h := newRackHarness(t, true)
	for ms := 0; ms < 5; ms++ {
		h.send(ms) // 1..5
	}
	h.ack(20, 0, 2, 3, 4) // three past 1
	require.False(t, h.sp.rack.reorderingSeen)
	require.Equal(t, []int{1}, h.lost())
}

// A retransmission that is lost too is declared lost a round trip later,
// once something sent after it has been delivered. libutp fast-retransmits
// a packet once and leaves a second loss to its retransmission timeout.
func TestRackLostRetransmissionIsDetected(t *testing.T) {
	scenario := func(h *rackHarness) []int {
		for ms := 0; ms < 5; ms++ {
			h.send(ms) // 1..5
		}
		h.ack(20, 0, 2, 3, 4, 5)
		first := h.lost()
		h.resend(1, 20) // lost again
		h.send(21)      // 6
		h.ack(41, 0, 2, 3, 4, 5, 6)
		return append(first, h.lost()...)
	}

	off := newRackHarness(t, false)
	require.Equal(t, []int{1}, scenario(off), "libutp's rule: one fast retransmission per packet")

	on := newRackHarness(t, true)
	require.Equal(t, []int{1, 1}, scenario(on))
	require.EqualValues(t, 2, on.sp.rack.losses)
	require.EqualValues(t, 1, on.sp.rack.relosts)
}

// One acknowledgement covering several packets retires them in no
// particular order -- its ack number before the packets under it -- and that
// is not reordering.
func TestRackInOrderAcksAreNotReordering(t *testing.T) {
	h := newRackHarness(t, true)
	for round := 0; round < 4; round++ {
		base := 20 * round
		a := h.send(base)
		h.send(base + 1)
		c := h.send(base + 2)
		_ = a
		h.ack(base+20, c)
	}
	require.False(t, h.sp.rack.reorderingSeen)
}

// A loss shown to be spurious widens the reordering window, for
// rackReoWndPersist recoveries.
func TestRackSpuriousLossWidensTheWindow(t *testing.T) {
	h := newRackHarness(t, true)
	h.send(0)
	h.ack(20, 1) // minimum round trip 20 ms
	h.sp.rack.reorderingSeen = true
	require.Equal(t, 5*time.Millisecond, h.sp.rackReoWnd(0))
	h.sp.rackOnSpuriousLoss()
	require.Equal(t, 10*time.Millisecond, h.sp.rackReoWnd(0))
	for i := 0; i < rackReoWndPersist; i++ {
		h.sp.rack.inRecovery = false
		p := h.sp.packets[0]
		h.sp.rackMarkLost(p, h.at(30))
		p.rackPending = false
		h.sp.rack.pending = 0
	}
	require.Equal(t, 5*time.Millisecond, h.sp.rackReoWnd(0))
}

// A packet past the highest one an acknowledgement reports is not judged:
// uTP's selective ack covers a fixed window past the ack number, so past it
// a packet may have arrived and not been said. Without the bound, a
// delivered resend -- sent after everything outstanding -- made all of them
// lost.
func TestRackDoesNotJudgePastWhatTheAckReports(t *testing.T) {
	h := newRackHarness(t, true)
	for ms := 0; ms < 40; ms++ {
		h.send(ms) // 1..40
	}
	h.ack(60, 0, 2, 3, 4) // 1 lost, three past it
	require.Equal(t, []int{1}, h.lost())
	h.resend(1, 60)
	// The resend fills the hole and the acknowledgement runs to 31; it says
	// nothing about 32..40, all sent before the resend.
	h.ack(120, 31) // past the minimum round trip, so the resend counts
	require.Empty(t, h.lost(), "32..40 were not reported either way")
}

// What was sent before what is decided by send order, not by the times
// recorded: those come from clocks read at different points of the event
// loop, and a packet sent after a resend can carry an earlier time than it.
func TestRackJudgesBySendOrderNotTimestamp(t *testing.T) {
	h := newRackHarness(t, true)
	for ms := 0; ms < 5; ms++ {
		h.send(ms) // 1..5
	}
	h.ack(20, 0, 3, 4, 5)
	require.Equal(t, []int{1, 2}, h.lost())
	h.resend(2, 30)
	h.resend(1, 29) // sent after 2's resend, stamped before it
	h.ack(70, 0, 2, 3, 4, 5)
	require.Empty(t, h.lost(), "1 was resent after the delivered resend of 2")
}
