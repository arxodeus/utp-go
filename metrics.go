package utp_go

import "time"

// ConnectionMetrics is a snapshot of one connection's state.
//
// It is what a test harness plots to tell a congestion controller that is
// working from one that merely is not crashing. Throughput alone cannot
// distinguish them.
type ConnectionMetrics struct {
	// At is when the snapshot was taken.
	At time.Time
	// Cid identifies the connection.
	Cid *ConnectionId

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
	// RTT is the smoothed round-trip estimate.
	RTT time.Duration
	// RTTVarianceMicros is the RTT variance estimate, in microseconds.
	RTTVarianceMicros int64
	// Timeout is the current retransmission timeout.
	Timeout time.Duration
	// BaseDelay is the lowest one-way delay seen in the delay window:
	// LEDBAT's estimate of the path with an empty queue.
	BaseDelay time.Duration
	// --- path MTU ---

	// MtuCurrent is the datagram size the search is currently sending, and
	// MtuFloor and MtuCeiling bound what it still has left to test. A
	// converged search has floor, current and ceiling equal.
	//
	// These are exposed because the search was previously invisible from
	// outside, which is how a defect in it survived: a fast retransmission
	// could be adopted as a probe, and losing it lowered the ceiling on
	// evidence about congestion rather than about size, so a lossy path drove
	// the packet size down for no reason. Nothing outside the connection could
	// see that happening.
	MtuCurrent uint32
	MtuFloor   uint32
	MtuCeiling uint32

	// PeerTsDiff is the most recent one-way delay measured from the peer's
	// timestamps -- the raw delay signal. Queueing delay is roughly
	// PeerTsDiff minus BaseDelay.
	PeerTsDiff time.Duration
	// TargetDelayMicros is the standing queue the controller aims for.
	TargetDelayMicros uint32

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

	// --- buffers ---

	// SendBufferPending is unsent application data still buffered.
	SendBufferPending int
	// RecvBufferPending is received data not yet read by the application.
	RecvBufferPending int
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

// QueueingDelay estimates the standing queue as the excess of the current
// one-way delay over the lowest one seen. This is the quantity LEDBAT exists
// to bound, and the number the M5 gates are judged on.
//
// It is only meaningful once BaseDelay has been established; before then it
// returns zero.
func (m ConnectionMetrics) QueueingDelay() time.Duration {
	if m.BaseDelay <= 0 || m.PeerTsDiff <= m.BaseDelay {
		return 0
	}
	return m.PeerTsDiff - m.BaseDelay
}

// MetricsObserver receives connection snapshots.
//
// It is called from the connection's own event-loop goroutine, which is what
// makes reading the connection's state race-free. It must not block, and must
// not call back into the stream or socket: doing either deadlocks the
// connection it is observing. Copy what you need and return.
type MetricsObserver func(ConnectionMetrics)

// DefaultMetricsInterval is how often metrics are sampled when an observer is
// set but no interval is given.
const DefaultMetricsInterval = 10 * time.Millisecond

func connStateName(s ConnStateType) string {
	switch s {
	case ConnConnecting:
		return "connecting"
	case ConnConnected:
		return "connected"
	case ConnClosed:
		return "closed"
	default:
		return "unknown"
	}
}
