# metrics.go

Every comment in `metrics.go` at e56c9aa, verbatim.

## Line 5 — type ConnectionMetrics

```
// ConnectionMetrics is a snapshot of one connection's state.
//
// It is what a test harness plots to tell a congestion controller that is
// working from one that merely is not crashing. Throughput alone cannot
// distinguish them.
```

## Line 11 — type ConnectionMetrics

```
// At is when the snapshot was taken.
```

## Line 13 — type ConnectionMetrics

```
// Cid identifies the connection.
```

## Line 16 — type ConnectionMetrics

```
// --- congestion control ---
```

## Line 18 — type ConnectionMetrics

```
// CwndBytes is the congestion window: what the controller believes the
	// path will carry.
```

## Line 21 — type ConnectionMetrics

```
// InFlightBytes is the volume the controller considers outstanding.
```

## Line 23 — type ConnectionMetrics

```
// MinCwndBytes is the floor the window will not drop below. A controller
	// pinned here has collapsed.
```

## Line 26 — type ConnectionMetrics

```
// PeerRecvWindow is the peer's advertised receive window. When this is
	// smaller than CwndBytes, flow control -- not congestion control -- is
	// what limits sending.
```

## Line 30 — type ConnectionMetrics

```
// RTT is the smoothed round-trip estimate: libutp's, in whole
	// milliseconds.
```

## Line 33 — type ConnectionMetrics

```
// FineRTT is the same estimate kept in microseconds, for measuring a
	// queue a millisecond or two deep.
```

## Line 36 — type ConnectionMetrics

```
// RTTVarianceMicros is the RTT variance estimate, in microseconds.
```

## Line 38 — type ConnectionMetrics

```
// Timeout is the current retransmission timeout.
```

## Line 40 — type ConnectionMetrics

```
// BaseDelay is the lowest one-way delay seen in the delay window:
	// LEDBAT's estimate of the path with an empty queue. CurrentDelay is the
	// newest sample of the same series.
	//
	// That series is the peer's measurement of *our* outgoing path -- the
	// timestamp_difference field it stamps on every packet, libutp's
	// `our_hist` (utp_internal.cpp:2016-2021). It is the direction this
	// sender's packets travel, and therefore the one LEDBAT controls.
	//
	// Not to be confused with PeerTsDiff below, which measures the other
	// direction entirely.
```

## Line 53 — type ConnectionMetrics

```
// ClockSkewCorrection is how much has been added to BaseDelay to cancel
	// clock drift between the two ends, detected from the peer's own base
	// delay falling. Zero on a pair of clocks running at the same rate.
```

## Line 58 — type ConnectionMetrics

```
// ClockDrift is the estimated slope of the delay the peer reports for our
	// packets, in microseconds per five-second slot, smoothed 7:1 towards its
	// history. libutp's clock_drift (utp_internal.cpp:2105).
	//
	// Negative means the reported delay is falling, which is what a peer whose
	// clock runs slow looks like. Past -200000 it earns a penalty; see
	// ClockDriftPenalty.
	//
	// This is a different quantity from ClockSkewCorrection above, and the two
	// are the two halves of libutp's drift handling: that one corrects the
	// measurement for ordinary drift between honest clocks, this one detects
	// drift far outside what honest hardware produces.
```

## Line 72 — type ConnectionMetrics

```
// ClockDriftPenalty is how much is currently being added to the measured
	// queueing delay because of ClockDrift. Zero on any ordinary path.
	//
	// Exposed so a test can assert the mechanism ran, rather than inferring it
	// from a throughput number that many other things also move.
```

## Line 78 — type ConnectionMetrics

```
// --- path MTU ---
```

## Line 80 — type ConnectionMetrics

```
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
```

## Line 94 — type ConnectionMetrics

```
// MtuProbesLostToDuplicateAcks counts probes the duplicate-acknowledgement
	// path concluded were too big for the path (libutp:1927-1940).
	//
	// Exposed for the same reason as the three above: it is the only way to
	// tell that mechanism ran, as opposed to the search having reached the
	// same size by some other route. A test that inferred it from where the
	// search settled would pass whether or not the code it names ever
	// executed.
```

## Line 104 — type ConnectionMetrics

```
// WindowReopenedAcks counts the acknowledgements this connection sent
	// because handing bytes up to the application reopened a receive window
	// narrower than what became free -- libutp's utp_read_drained
	// (utp_internal.cpp:3242-3261).
	//
	// Zero on a connection whose reader keeps up, which is most of them: the
	// acknowledgement owed for incoming data already carries a current window,
	// because the drain happens before the acknowledgement in the same pass of
	// the event loop. It is non-zero exactly when the peer would otherwise not
	// have been told.
```

## Line 116 — type ConnectionMetrics

```
// PeerTsDiff is the one-way delay this end measured from the peer's
	// timestamps: how long the peer's last packet took to arrive here. It is
	// what gets echoed back in timestamp_difference_microseconds, and it is
	// libutp's `their_delay` (utp_internal.cpp:2000-2001).
	//
	// It is the *inbound* path. BaseDelay and CurrentDelay are the outbound
	// one. Subtracting across the two is meaningless, which is what
	// QueueingDelay used to do -- see the note there.
```

## Line 125 — type ConnectionMetrics

```
// TargetDelayMicros is the standing queue the controller aims for.
```

## Line 127 — type ConnectionMetrics

```
// SlowStart reports whether the controller is still in slow start, where
	// the window grows whether or not the application is filling it. Without
	// this, a window growing during an idle period cannot be told from the
	// application-limited guard failing.
```

## Line 132 — type ConnectionMetrics

```
// AppLimitedSince is how long since the sender last had data to send and
	// no window to send it in. Once this passes a second the controller stops
	// growing the window, on the grounds that a window nothing fills measures
	// nothing. Zero means the window has never been filled.
```

## Line 138 — type ConnectionMetrics

```
// --- cumulative counters ---
```

## Line 140 — type ConnectionMetrics

```
// PacketsSent and BytesSent count every transmission, retransmissions
	// included.
```

## Line 144 — type ConnectionMetrics

```
// PacketsRetransmitted and BytesRetransmitted count only retransmissions.
	// The ratio to PacketsSent is the retransmit rate.
```

## Line 148 — type ConnectionMetrics

```
// PacketsReceived and BytesReceived count packets accepted from the peer.
```

## Line 151 — type ConnectionMetrics

```
// Timeouts counts retransmission-timeout events.
```

## Line 153 — type ConnectionMetrics

```
// FastRetransmits counts packets resent because they were declared lost
	// by duplicate acks rather than by a timeout.
```

## Line 156 — type ConnectionMetrics

```
// LossProbes counts packets resent by the loss probe: nothing had been
	// acknowledged for a probe timeout, short of the retransmission timeout.
	// Not a libutp mechanism; see DEVIATIONS.md.
```

## Line 160 — type ConnectionMetrics

```
// RackLosses counts packets ConnectionConfig.RACK's loss detection
	// declared lost, and RackRelosts those of them that were already
	// retransmissions. Zero with RACK off.
```

## Line 166 — type ConnectionMetrics

```
// --- buffers ---
```

## Line 168 — type ConnectionMetrics

```
// SendBufferPending is unsent application data still buffered.
```

## Line 170 — type ConnectionMetrics

```
// RecvBufferPending is received data not yet read by the application,
	// contiguous or held behind a gap.
```

## Line 173 — type ConnectionMetrics

```
// RecvBufferReadable is the contiguous part of it: what can be handed up
	// now. The difference between the two is data held behind a gap, which
	// occupies the buffer and cannot be delivered until the gap is filled.
	//
	// Exposed because the two being far apart is the signature of a receiver
	// wedged behind a hole, and telling that apart from a slow reader is not
	// possible from Pending alone.
```

## Line 181 — type ConnectionMetrics

```
// RecvBufferDrops counts data packets refused because the receive buffer
	// had no room for them. Each one is a gap the peer has to fill with a
	// retransmission, so a non-zero figure on a link that is not dropping
	// anything means this end told the peer it had room it did not have.
```

## Line 186 — type ConnectionMetrics

```
// PendingWrites is the number of queued write requests.
```

## Line 189 — type ConnectionMetrics

```
// State is the connection's state: "connecting", "connected" or "closed".
```

## Line 193 — func (ConnectionMetrics).RetransmitRate

```
// RetransmitRate is the fraction of transmissions that were retransmissions.
```

## Line 201 — func (ConnectionMetrics).QueueingDelay

```
// QueueingDelay estimates the standing queue on the path this connection is
// sending into, as the excess of the current one-way delay over the lowest one
// seen. This is the quantity LEDBAT exists to bound.
//
// Both terms come from the same series and the same direction -- the peer's
// measurement of our outbound path. The difference between two directions'
// delays is not a queue in either of them.
//
// It is only meaningful once BaseDelay has been established; before then it
// returns zero.
```

## Line 218 — type MetricsObserver

```
// MetricsObserver receives connection snapshots.
//
// It is called with the connection's lock held, from whichever goroutine is
// running the connection at the time -- its own event loop, or the socket's
// reader handling a packet for it -- which is what makes reading the
// connection's state race-free. It must not block, and must not call back into
// the stream or socket: blocking holds up every connection on the socket, and
// calling back can deadlock. Copy what you need and return.
```

## Line 228 — const DefaultMetricsInterval

```
// DefaultMetricsInterval is how often metrics are sampled when an observer is
// set but no interval is given.
```
