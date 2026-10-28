package utp

import "time"

// ConnectionMetrics is a snapshot of one connection's state.
type ConnectionMetrics struct {
	// At is when the snapshot was taken.
	At time.Time
	// CID identifies the connection.
	CID *ConnectionID

	// --- congestion control ---

	// CwndBytes is the congestion window: what the controller believes the
	// path will carry.
	CwndBytes uint32
	// InFlightBytes is the volume the controller considers outstanding.
	InFlightBytes uint32
	// MinCwndBytes is the floor the window will not drop below. A controller
	// pinned here has collapsed.
	MinCwndBytes uint32
	// PeerRecvWindow is the peer's advertised receive window. When this is
	// smaller than CwndBytes, flow control -- not congestion control -- is
	// what limits sending.
	PeerRecvWindow uint32
	// RTT is the smoothed round-trip estimate, in whole milliseconds.
	RTT time.Duration
	// FineRTT is the same estimate kept in microseconds, for measuring a
	// queue a millisecond or two deep.
	FineRTT time.Duration
	// RTTVarianceMicros is the RTT variance estimate, in microseconds.
	RTTVarianceMicros int64
	// Timeout is the current retransmission timeout.
	Timeout time.Duration
	// BaseDelay is the lowest one-way delay seen in the delay window:
	// LEDBAT's estimate of the path with an empty queue. CurrentDelay is the
	// newest sample of the same series.
	//
	// Both measure the outbound path, as reported by the peer in the
	// timestamp_difference field; that is the direction LEDBAT controls.
	// PeerTimestampDiff measures the inbound path.
	BaseDelay    time.Duration
	CurrentDelay time.Duration
	// ClockSkewCorrection is how much has been added to BaseDelay to cancel
	// clock drift between the two ends, detected from the peer's own base
	// delay falling. Zero on a pair of clocks running at the same rate.
	ClockSkewCorrection time.Duration

	// ClockDrift is the estimated slope of the delay the peer reports for
	// our packets, in microseconds per five-second slot, smoothed 7:1
	// towards its history. A negative value means the reported delay is
	// falling, as when the peer's clock runs slow; below -200000 it incurs
	// a penalty (ClockDriftPenalty).
	//
	// ClockSkewCorrection corrects for ordinary drift between honest
	// clocks; ClockDrift detects drift far outside what hardware produces.
	ClockDrift int64

	// ClockDriftPenalty is how much is currently added to the measured
	// queuing delay because of ClockDrift. It is zero on any ordinary path.
	ClockDriftPenalty time.Duration
	// --- path MTU ---

	// MtuCurrent is the datagram size the path-MTU search is currently
	// sending, and MtuFloor and MtuCeiling bound the sizes it has yet to
	// test. A converged search has all three equal.
	MtuCurrent uint32
	MtuFloor   uint32
	MtuCeiling uint32

	// MtuProbesLostToDuplicateAcks counts probes that duplicate
	// acknowledgements showed were too large for the path.
	MtuProbesLostToDuplicateAcks uint64

	// WindowReopenedAcks counts acknowledgements sent because delivering
	// data to the application opened a receive window larger than the one
	// last advertised. It is zero while the reader keeps up, since the
	// acknowledgement for incoming data already carries a current window.
	WindowReopenedAcks uint64

	// PeerTimestampDiff is the one-way delay measured from the peer's timestamps:
	// how long the peer's last packet took to arrive. It is echoed back in
	// timestamp_difference_microseconds. It measures the inbound path,
	// whereas BaseDelay and CurrentDelay measure the outbound one.
	PeerTimestampDiff time.Duration
	// TargetDelayMicros is the standing queue the controller aims for.
	TargetDelayMicros uint32
	// SlowStart reports whether the controller is still in slow start.
	SlowStart bool
	// AppLimitedSince is how long since the sender last had data to send
	// and no window to send it in. After a second the controller stops
	// growing the window, since an unfilled window measures nothing. Zero
	// means the window has never been filled.
	AppLimitedSince time.Duration

	// --- cumulative counters ---

	// PacketsSent and BytesSent count every transmission, retransmissions
	// included.
	PacketsSent uint64
	BytesSent   uint64
	// PacketsRetransmitted and BytesRetransmitted count only retransmissions.
	// The ratio to PacketsSent is the retransmit rate.
	PacketsRetransmitted uint64
	BytesRetransmitted   uint64
	// PacketsReceived and BytesReceived count packets accepted from the peer.
	PacketsReceived uint64
	BytesReceived   uint64
	// Timeouts counts retransmission-timeout events.
	Timeouts uint64
	// FastRetransmits counts packets resent because they were declared lost
	// by duplicate acks rather than by a timeout.
	FastRetransmits uint64
	// LossProbes counts packets resent by the loss probe because nothing had
	// been acknowledged for a probe timeout, shorter than the retransmission
	// timeout.
	LossProbes uint64
	// RackLosses counts packets declared lost by RACK loss detection, and
	// RackRelosts those that were already retransmissions. Both are zero
	// with RACK off.
	RackLosses  uint64
	RackRelosts uint64

	// --- buffers ---

	// SendBufferPending is unsent application data still buffered.
	SendBufferPending int
	// RecvBufferPending is received data not yet read by the application,
	// contiguous or held behind a gap.
	RecvBufferPending int
	// RecvBufferReadable is the contiguous part of RecvBufferPending, which
	// can be delivered now. The difference is data held behind a gap; a
	// large difference indicates a receiver waiting on a missing packet
	// rather than a slow reader.
	RecvBufferReadable int
	// RecvBufferDrops counts data packets refused because the receive
	// buffer had no room. Each is a gap the peer must fill by
	// retransmission.
	RecvBufferDrops uint64
	// PendingWrites is the number of queued write requests.
	PendingWrites int

	// State is the connection's state: "connecting", "connected" or "closed".
	State string
}

// RetransmitRate is the fraction of transmissions that were retransmissions.
func (m ConnectionMetrics) RetransmitRate() float64 {
	if m.PacketsSent == 0 {
		return 0
	}
	return float64(m.PacketsRetransmitted) / float64(m.PacketsSent)
}

// QueueingDelay estimates the standing queue on the outbound path: the
// current one-way delay minus the lowest seen. This is the quantity LEDBAT
// bounds. It returns zero until BaseDelay has been established.
func (m ConnectionMetrics) QueueingDelay() time.Duration {
	if m.BaseDelay <= 0 || m.CurrentDelay <= m.BaseDelay {
		return 0
	}
	return m.CurrentDelay - m.BaseDelay
}

// MetricsObserver receives connection snapshots.
//
// It is called with the connection's lock held, from whichever goroutine is
// running the connection: its event loop, or the socket's reader. It must
// not block and must not call into the stream or socket: blocking stalls
// every connection on the socket, and calling back can deadlock. Copy what
// is needed and return.
type MetricsObserver func(ConnectionMetrics)

// DefaultMetricsInterval is how often metrics are sampled when an observer is
// set but no interval is given.
const DefaultMetricsInterval = 10 * time.Millisecond

func connStateName(s connStateType) string {
	switch s {
	case connConnecting:
		return "connecting"
	case connConnected:
		return "connected"
	case connClosed:
		return "closed"
	default:
		return "unknown"
	}
}
