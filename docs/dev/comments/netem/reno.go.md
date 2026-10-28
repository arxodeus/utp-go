# netem/reno.go

Every comment in `netem/reno.go` at e56c9aa, verbatim.

## Line 14

```
// A loss-based, Reno-shaped competing flow.
//
// LEDBAT exists to yield to TCP. Nothing in this harness could test that,
// because there was no TCP-like traffic to yield to -- every measurement was
// uTP against uTP, which is the easy case and not the one the protocol is
// judged on. This provides the other side of that experiment.
//
// # What this is
//
// A sender and receiver implementing TCP Reno's congestion control, exactly:
// slow start, congestion avoidance at one segment per round trip, fast
// retransmit on three duplicate acknowledgements, fast recovery halving the
// window, and a retransmission timeout that collapses it. The round-trip
// estimator is Jacobson/Karels, as RFC 6298 specifies, with Karn's algorithm.
//
// Congestion control is the whole point: what makes TCP the traffic uTP must
// defer to is that it fills the bottleneck queue and only backs off when
// packets are dropped. That behaviour is reproduced faithfully.
//
// # What this is not
//
// It is not TCP. There is no TCP header, no handshake, no options, no window
// scaling, no SACK, no Nagle, no delayed acknowledgements, no PAWS, no
// checksums, and no interoperability with anything. The receiver acknowledges
// every packet immediately; a real TCP receiver acknowledges every second
// packet or on a delay timer, which makes real TCP grow marginally more
// slowly than this does.
//
// So a result from it should be read as "against a loss-based sender that
// fills the queue", which is the property that matters here, and not as
// "against Linux TCP". Where this file's behaviour is more aggressive than
// real TCP -- the acknowledgement policy is the one place it is -- a uTP flow
// measured against it is being tested slightly harder than reality, which is
// the safe direction for a deference claim.
```

## Line 50 — const renoHeaderBytes, renoInitialWindowSegments, renoDupAcksBeforeRetransmit...

```
// renoHeaderBytes is a sequence number and a flag.
```

## Line 52 — const renoHeaderBytes, renoInitialWindowSegments, renoDupAcksBeforeRetransmit...

```
// renoInitialWindowSegments is TCP's initial window. RFC 6928 raised the
	// usual value to 10; the conservative 2 is used here so the comparison is
	// not decided by the first round trip.
```

## Line 56 — const renoHeaderBytes, renoInitialWindowSegments, renoDupAcksBeforeRetransmit...

```
// renoDupAcksBeforeRetransmit is Reno's three duplicate acknowledgements.
```

## Line 58 — const renoHeaderBytes, renoInitialWindowSegments, renoDupAcksBeforeRetransmit...

```
// renoMinRTO is RFC 6298's one-second floor, relaxed to 200ms because the
	// emulated paths here are tens of milliseconds and a one-second floor
	// would make a timeout dominate every measurement. Documented rather than
	// silent: it makes this sender recover from loss faster than a
	// standards-compliant TCP would.
```

## Line 73 — type RenoResult

```
// RenoResult is what a completed Reno flow achieved.
```

## Line 82 — type RenoResult

```
// CwndMaxBytes and CwndMeanBytes describe the congestion window over the
	// transfer, sampled once per acknowledgement.
```

## Line 86 — type RenoResult

```
// Delivered is the receiver's in-order byte count over time, sampled at
	// most every deliverySampleInterval, so a split against another flow can
	// be taken over only the time both were running. See SplitWhileBothRan.
```

## Line 92 — func RunRenoFlow

```
// RunRenoFlow sends payloadBytes from sender to receiver over the emulated
// network and returns what it achieved. It blocks until the transfer
// completes or ctx is done.
//
// mss is the segment size, excluding this protocol's 8-byte header.
```

## Line 106 — func RunRenoFlow

```
// Byte offsets are uint32 on the wire here; a larger payload would wrap
	// them and the sender would flood rather than fail.
```

## Line 154 — type renoSender

```
// retransmitted marks sequence numbers whose round trip must not be
	// sampled: Karn's algorithm.
```

## Line 195 — func (*renoSender).run

```
// Send whatever the window allows.
```

## Line 227 — func (*renoSender).run

```
// Tell the receiver we are finished, so it can report.
```

## Line 267 — func (*renoSender).onAck

```
// A duplicate acknowledgement: the receiver got something out of
		// order, so something ahead of it was lost.
```

## Line 271 — func (*renoSender).onAck

```
// Fast retransmit and fast recovery: halve the window and resend
			// the segment the receiver is waiting for.
```

## Line 281 — func (*renoSender).onAck

```
// New data acknowledged.
```

## Line 303 — func (*renoSender).onAck

```
// Slow start: one segment per acknowledged segment, so the window
		// doubles each round trip.
```

## Line 307 — func (*renoSender).onAck

```
// Congestion avoidance: one segment per round trip.
```

## Line 312 — func (*renoSender).resendFrom

```
// resendFrom retransmits the segment the receiver is waiting for.
```

## Line 326 — func (*renoSender).onTimeout

```
// RFC 5681: collapse to one segment, halve the threshold, back off the
	// timer.
```

## Line 335 — func (*renoSender).onTimeout

```
// Everything unacknowledged is presumed lost; go back to the last
	// acknowledged byte.
```

## Line 343 — func (*renoSender).sampleRTT

```
// sampleRTT is RFC 6298's estimator.
```

## Line 365 — func renoReceiver

```
// renoReceiver acknowledges every packet with the highest contiguous byte
// received, and reports the total when the sender says it is done.
```

## Line 398 — func renoReceiver

```
// Absorb anything that arrived early and is now in order.
```
