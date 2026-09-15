package netem

import (
	"encoding/binary"
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// DriftingClock wraps a Conn so that the packets leaving it carry timestamps
// from a clock running at a different rate from the receiver's.
//
// It exists because nothing in this repository could produce clock skew at
// all. Both endpoints of an emulated network read the same process clock, so
// every delay measurement was taken between two perfectly synchronised
// peers -- which is the one condition under which a delay-based congestion
// controller has no drift problem to solve.
//
// That matters because LEDBAT's whole signal is a one-way delay, and a
// one-way delay cannot be measured without comparing two clocks. The sender
// stamps its own clock into every packet; the receiver subtracts that from
// its own and reports the difference back. If the two clocks run at different
// rates, the difference contains a systematic error that grows without bound,
// and the controller reads it as a queue.
//
// libutp is explicit about this, in DelayHist.add_sample:
//
//	// The two clocks (in the two peers) are assumed not to
//	// progress at the exact same rate. They are assumed to be
//	// drifting, which causes the delay samples to contain
//	// a systematic error, either they are under-
//	// estimated or over-estimated. This is why we update the
//	// delay_base every two minutes, to adjust for this.
//	                                        (utp_internal.cpp:293-298)
//
// # Which direction hurts
//
// A clock running **slow** is the dangerous one. The sender stamps a time
// that falls further and further behind, so the receiver's `now - stamp`
// grows, and the growth is reported back to the sender as its own outbound
// delay. The sender sees a queue that is not there and closes its window.
//
// A clock running fast produces the mirror image, and is harmless: the
// measured delay shrinks, and the base delay -- a minimum -- follows it down
// immediately.
//
// # What this is not
//
// It models rate error, not offset. A constant offset between two clocks
// cancels out of LEDBAT entirely, because the controller works in the
// difference between the current delay and the lowest delay seen, and a
// constant appears in both. Only a changing offset survives that subtraction,
// which is why this is expressed in parts per million rather than in
// milliseconds.
type DriftingClock struct {
	utp.Conn

	// ppm is the rate error in parts per million. Negative means the clock
	// loses time. Real crystals sit within +/-100ppm; a virtual machine whose
	// host is under load can be far worse, which is the case worth modelling.
	ppm float64

	mu    sync.Mutex
	epoch time.Time
}

// NewDriftingClock wraps conn with a clock drifting at ppm parts per million.
// Negative ppm loses time, which is the direction that inflates the peer's
// view of this sender's delay.
func NewDriftingClock(conn utp.Conn, ppm float64) *DriftingClock {
	return &DriftingClock{Conn: conn, ppm: ppm, epoch: time.Now()}
}

// utpTimestampOffset is where a uTP v1 header carries the sender's clock, in
// microseconds: after the type/version byte, the extension byte and the
// two-byte connection id (packet.go, PacketHeaderV1.EncodeToBytes).
const utpTimestampOffset = 4

// skewNow is the error this clock has accumulated since the connection began.
func (d *DriftingClock) skewNow() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Duration(float64(time.Since(d.epoch)) * d.ppm / 1e6)
}

// WriteTo rewrites the outgoing packet's timestamp as the drifting clock
// would have stamped it, then sends it.
func (d *DriftingClock) WriteTo(b []byte, dst utp.ConnectionPeer) (int, error) {
	if len(b) >= utpTimestampOffset+4 {
		// Copied rather than patched in place: the caller owns b, and the
		// connection above reuses its packet buffers for retransmission. A
		// patch would compound the skew every time a packet went out again.
		out := make([]byte, len(b))
		copy(out, b)
		addToTimestamp(out, d.skewNow())
		b = out
	}
	return d.Conn.WriteTo(b, dst)
}

// ReadFrom adjusts the arriving packet's timestamp by the opposite amount, so
// that the delay this host measures is the delay its own drifting clock would
// have produced.
//
// **This is half the model, and leaving it out makes the model wrong in a way
// that is easy to miss.** A host does not have one clock for writing
// timestamps and another for reading them. If its clock runs slow, the times
// it stamps into its own packets fall behind *and* the `now - peer_timestamp`
// it computes for arriving packets shrinks by exactly as much. The first half
// is what inflates the peer's view of this sender and creates the phantom
// queue; the second is the only evidence from which clock drift can be
// told apart from a real queue, and it is what libutp's their_hist watches
// (utp_internal.cpp:2002-2014).
//
// Modelling only the first half produces a perfectly convincing phantom queue
// and no way for any correction to notice it. This wrapper did exactly that
// at first, and the correction built against it did nothing at all -- for the
// right reason, which took a while to see.
//
// The sign is the mirror of WriteTo's: a clock slow by s stamps `t - s` on
// the way out, and measures `now - ts` as `true - s` on the way in, which is
// the same as adding s to the timestamp it reads.
func (d *DriftingClock) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	n, peer, err := d.Conn.ReadFrom(b)
	if err != nil || n < utpTimestampOffset+4 {
		return n, peer, err
	}
	addToTimestamp(b[:n], -d.skewNow())
	return n, peer, err
}

// addToTimestamp adds d to the uTP timestamp field of an encoded packet,
// wrapping as the 32-bit microsecond field does.
func addToTimestamp(pkt []byte, d time.Duration) {
	ts := binary.BigEndian.Uint32(pkt[utpTimestampOffset:])
	binary.BigEndian.PutUint32(pkt[utpTimestampOffset:], ts+uint32(d.Microseconds()))
}
