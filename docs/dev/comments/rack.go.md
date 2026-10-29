# rack.go

Every comment in `rack.go` at e56c9aa, verbatim.

## Line 5

```
// RACK loss detection: RFC 8985's time-based rule, behind
// ConnectionConfig.RACK and off by default. Off, nothing here runs and loss
// detection is libutp's: a packet is lost once three packets after it have
// been acknowledged, and each is fast-retransmitted once (DetectLostPackets,
// fastResendSeqNum). See DEVIATIONS.md, "RACK loss detection (opt-in)".
//
// On, it replaces that rule. A packet is lost once a packet sent after it
// has been delivered and a round trip plus a reordering window has passed
// since it was sent (RFC 8985 section 6.2). It differs from the count in
// three ways that matter here:
//
//   - reordering within the window is not taken for loss, where three later
//     packets arriving first was enough;
//   - a retransmission can be declared lost too, a round trip after it was
//     sent, where libutp leaves a lost fast retransmission to its timeout;
//   - the decision is by time, so it is made by a timer when no
//     acknowledgement arrives to make it.
//
// uTP has no DSACK, which RFC 8985 uses to widen the reordering window after
// a spurious retransmission. The undo's detection stands in for it: a cut
// shown to be for a packet that was only late (checkSpuriousLoss) widens the
// window the same way.
```

## Line 28 — const rackReoWndPersist

```
// rackReoWndPersist is how many recoveries the reordering window stays
// widened for after a spurious retransmission: RFC 8985's
// RACK.reo_wnd_persist.
```

## Line 33 — type rackState

```
// rackState is RFC 8985's per-connection state, kept by sentPackets.
```

## Line 37 — type rackState

```
// xmitTs and endSeq are the send time and sequence number of the most
	// recently sent packet that has been delivered (RACK.xmit_ts,
	// RACK.end_seq); rtt is the round trip measured to it (RACK.rtt).
```

## Line 44 — type rackState

```
// xmitOrder is the same packet's place in the send order
	// (sentPacket.sentOrder), which decides what was sent before it. Not
	// the send times: those come from clocks read at different points of
	// an event-loop pass, so a packet sent after a resend could carry an
	// earlier time than it, and be taken for one sent before it and lost.
	// Measured on a slow-start overflow: the two packets at the top of the
	// selective ack declared lost in flight, every time a resend was
	// acknowledged.
```

## Line 54 — type rackState

```
// minRTT is the lowest round trip from a packet sent once.
```

## Line 57 — type rackState

```
// fack is the highest sequence number delivered without having been
	// retransmitted (RACK.fack) by earlier acknowledgements; a packet
	// delivered below it arrived out of order, and reorderingSeen records
	// that it has happened. ackFack is the same for the acknowledgement
	// being processed, folded into fack when it is done: one
	// acknowledgement retires the packets it covers in no particular
	// order (the cumulative ack number before the packets under it), and
	// those are not reordered.
```

## Line 71 — type rackState

```
// reoWndMult and reoWndPersist widen the reordering window after a
	// spurious retransmission, for rackReoWndPersist recoveries.
```

## Line 76 — type rackState

```
// inRecovery is set when a loss is declared and cleared once the
	// cumulative acknowledgement passes recoveryEnd, the last packet sent
	// when it began.
```

## Line 82 — type rackState

```
// coverEnd is the highest sequence number the last acknowledgement
	// reported delivered: its ack number, or its highest selective-ack bit.
	// Below it the acknowledgement speaks for every packet; past it a packet
	// may well have arrived and not been said. uTP's selective ack is a
	// fixed window past the ack number -- 30 packets in libutp's and ours,
	// in a 32-bit field whose last two bits are padding, and fewer from a
	// libutp peer with a small receive buffer -- where TCP's blocks reach
	// anywhere, and RFC 8985 assumes they do. Without the bound, a two-flow
	// slow-start overflow that dropped 22 packets had 101 declared lost:
	// everything past the window sent before a delivered resend.
```

## Line 94 — type rackState

```
// pending counts packets declared lost and not yet resent.
```

## Line 97 — type rackState

```
// timeout is how long until the next packet still inside its reordering
	// window would pass it; zero when none is. Set by rackDetect, for the
	// connection to arm its timer.
```

## Line 102 — type rackState

```
// packets declared lost
```

## Line 103 — type rackState

```
// of those, retransmissions declared lost again
```

## Line 106 — func (*sentPackets).rackOnDelivered

```
// rackOnDelivered is RFC 8985's RACK_update (section 6.2, steps 1-3) for a
// packet this acknowledgement delivered for the first time.
```

## Line 126 — func (*sentPackets).rackOnDelivered

```
// Acknowledged sooner after the resend than any round trip has
		// taken: the original's acknowledgement, most likely, and no
		// evidence about when the resend was delivered (step 2).
```

## Line 137 — func (*sentPackets).rackOnAckNum

```
// rackOnAckNum ends a recovery once the cumulative acknowledgement has
// passed everything sent before it began.
```

## Line 146 — func (*sentPackets).rackOnSpuriousLoss

```
// rackOnSpuriousLoss widens the reordering window, as RFC 8985 does on a
// DSACK (section 6.2, step 4), and records that the path reorders: a loss
// shown to be spurious is a packet that arrived after later ones.
//
// Without the second, RACK could go a whole transfer without a window. Its
// other evidence of reordering is an original transmission acknowledged
// below one sent after it (rackOnDelivered), and on a path that reorders
// every packet past three others, each was declared lost and resent before
// its original arrived, so no original ever counted: one run in ten of
// netem.TestRackResendsLessUnderReordering resent 25 packets, as libutp's
// rule does, against 3 in the others.
```

## Line 166 — func (*sentPackets).rackReoWnd

```
// rackReoWnd is RFC 8985's RACK_update_reo_wnd (section 6.2, step 4): no
// window until reordering has been seen, once three packets have been
// selectively acknowledged or a recovery is under way -- which makes the
// rule libutp's count until the path is seen to reorder -- and otherwise a
// quarter of the minimum round trip, widened after spurious losses, never
// more than the smoothed round trip.
```

## Line 184 — func (*sentPackets).rackDetect

```
// rackDetect is RFC 8985's RACK_detect_loss (section 6.2, step 5): the
// outstanding packets now lost, and in rack.timeout how long until the next
// would be.
```

## Line 218 — func (*sentPackets).rackDetect

```
// nothing reported this far, either way
```

## Line 225 — func (*sentPackets).rackDetect

```
// this transmission is already counted lost
```

## Line 228 — func (*sentPackets).rackDetect

```
// not sent before anything delivered
```

## Line 239 — func (*sentPackets).rackMarkLost

```
// rackMarkLost queues a packet RACK found lost for resending and charges the
// loss to the congestion controller.
```

## Line 263 — func (*sentPackets).rackDetectAndMark

```
// rackDetectAndMark declares what RACK finds lost now. It reports whether
// anything was.
```

## Line 273 — func (*sentPackets).rackTakeLost

```
// rackTakeLost is TakeLostPackets under RACK: the packets declared lost,
// oldest first, at most maxFastResendsPerAck, retransmissions included.
```

## Line 297 — func (*sentPackets).rackOnAck

```
// rackOnAck notes how far an acknowledgement reports: see coverEnd.
```
