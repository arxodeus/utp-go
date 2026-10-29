# netem/link.go

Every comment in `netem/link.go` at e56c9aa, verbatim.

## Line 10 — type DropCause

```
// DropCause records why the link discarded a packet.
```

## Line 14 — const DropLoss, DropQueueOverflow, DropReceiverOverflow

```
// DropLoss is loss on the medium, from Config.LossRate.
```

## Line 16 — const DropLoss, DropQueueOverflow, DropReceiverOverflow

```
// DropQueueOverflow is congestion loss: the bottleneck queue was full.
```

## Line 18 — const DropLoss, DropQueueOverflow, DropReceiverOverflow

```
// DropReceiverOverflow means the packet arrived but the destination's
	// inbox was full -- the reader is not keeping up. This models a kernel
	// receive-buffer overflow, not a network event.
```

## Line 24 — func (DropCause).String

```
// String names the drop cause.
```

## Line 38 — type scheduled

```
// scheduled is a packet with its delivery time.
```

## Line 42 — type scheduled

```
// seq orders packets scheduled for the same instant, so the heap is a
	// stable FIFO rather than depending on heap internals.
```

## Line 45 — type scheduled

```
// queueDelay is how long this packet waited for the bottleneck.
```

## Line 47 — type scheduled

```
// sizeBytes is retained so the queue accounting can be released on
	// departure.
```

## Line 50 — type scheduled

```
// src and dst are this packet's endpoints. They are per packet rather
	// than per link because one Link can be shared by several endpoint
	// pairs -- that is what makes a shared bottleneck, where two flows
	// contend for one queue and one rate. See Network.ConnectShared.
```

## Line 77 — type Link

```
// Link is one direction of an emulated path. Traffic from several flows
// between the same pair of endpoints shares one Link, and therefore contends
// for its bandwidth and its queue.
```

## Line 87 — type Link

```
// nextFree is when the bottleneck finishes transmitting whatever is
	// currently being serialized. Serialization is what creates a queue.
```

## Line 90 — type Link

```
// queuedBytes is the backlog currently occupying the bottleneck queue.
```

## Line 97 — type Link

```
// samples records queueing delay over time for post-run analysis.
```

## Line 116 — const icmpHeaderOverhead

```
// icmpHeaderOverhead is what a real IPv4 path spends on headers below the UDP
// payload: 20 bytes of IP and 8 of UDP. This emulator's MTU bounds the uTP
// datagram, so a report quoting a *link* MTU has to add them back.
```

## Line 121 — func (*Link).enqueueWithFlags

```
// enqueueWithFlags applies the link model to a packet and schedules its
// delivery, given whether the sender forbade fragmentation. It never blocks.
```

## Line 139 — func (*Link).enqueueWithFlags

```
// 0. Size. A datagram larger than the path will carry is dropped before
	//    anything else, because it never gets onto the wire at all and its
	//    loss carries no information about congestion. This is what makes
	//    path-MTU discovery testable: the search's ceiling only comes down
	//    when a probe is refused for being too big.
```

## Line 145 — func (*Link).enqueueWithFlags

```
// An IPv4 router with something too big for the next hop has two
		// choices, and which one it takes is exactly what the don't-fragment
		// bit decides. Without FragmentOversized this link only ever makes the
		// second choice, which models IPv6 -- correct, and the reason the
		// don't-fragment bit made no difference here until now.
```

## Line 151 — func (*Link).enqueueWithFlags

```
// Fragmented and forwarded. It arrives, so the sender learns
			// nothing about the path being narrow -- which is the trap: an
			// MTU probe sent without the bit is acknowledged, the search's
			// floor rises, and it settles on a size that only works because
			// every packet at it is being fragmented.
			//
			// Counted, not dropped: execution falls through to the rest of
			// the pipeline, so a fragmented datagram is still subject to
			// loss, queueing and delay like any other. Nothing here models
			// the *cost* of fragmentation -- a real path pays in headers and
			// in a whole datagram lost when any one fragment is -- because
			// what is being tested is what the search concludes, and it
			// concludes it from the packet arriving at all.
```

## Line 171 — func (*Link).enqueueWithFlags

```
// The router tells the sender why. Called outside the lock: the
			// callback reaches back into a uTP socket, which must not be able
			// to deadlock against this link.
			//
			// The datagram is copied because the caller's buffer is reused,
			// and the quoted MTU is the link MTU an ICMP message would carry:
			// this emulator's MTU limits the uTP datagram, so the headers a
			// real path also carries are added back.
```

## Line 187 — func (*Link).enqueueWithFlags

```
// 1. Loss on the medium. Drawn before queueing so a lost packet does not
	//    occupy the bottleneck -- it never made it onto the wire.
```

## Line 196 — func (*Link).enqueueWithFlags

```
// 2. Bottleneck serialization and queueing.
```

## Line 203 — func (*Link).enqueueWithFlags

```
// Release backlog that has drained since the last packet.
```

## Line 208 — func (*Link).enqueueWithFlags

```
// Bytes still ahead of us in the queue, approximated from the
			// remaining busy time at the link rate.
```

## Line 215 — func (*Link).enqueueWithFlags

```
// Tail drop: this is congestion loss, the signal a congestion
			// controller is meant to respond to.
```

## Line 231 — func (*Link).enqueueWithFlags

```
// 3. Propagation delay, plus jitter.
```

## Line 234 — func (*Link).enqueueWithFlags

```
// Uniform in [-Jitter, +Jitter].
```

## Line 241 — func (*Link).enqueueWithFlags

```
// 4. Reordering: displace this packet behind those that follow it.
```

## Line 286 — func (*Link).enqueueWithFlags

```
// Nudge the delivery goroutine: the new packet may be due before whatever
	// it is currently waiting for.
```

## Line 294 — func (*Link).run

```
// run delivers scheduled packets at their arrival times.
```

## Line 301 — func (*Link).run

```
// Deliver everything already due, in one pass. Batching matters: at
		// high rates packets are spaced far more finely than the runtime's
		// timer granularity, so waking per packet would not keep up.
```
