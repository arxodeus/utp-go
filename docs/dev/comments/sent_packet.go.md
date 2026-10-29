# sent_packet.go

Every comment in `sent_packet.go` at e56c9aa, verbatim.

## Line 10 — var lostFreeList

```
// lostFreeList is the node free list every connection's lost-packet tree
// shares, as the receive buffers' reorder trees do (pendingFreeList).
```

## Line 33 — type sentPacket

```
// acked is whether the packet has been acknowledged. It was a slice of
	// the times it was, appended to on the first and only ever asked
	// whether it was empty: an allocation per packet for a boolean.
```

## Line 37 — type sentPacket

```
// needResend is libutp's need_resend: given up as lost by a
	// retransmission timeout and waiting to be sent again. See
	// MarkAllForResend.
```

## Line 41 — type sentPacket

```
// rackLostAt is the transmission RACK last declared lost, and
	// rackPending whether it is waiting to be resent. See rack.go.
```

## Line 45 — type sentPacket

```
// sentOrder is when this packet was last sent, as a count of the
	// transmissions before it: its true place in the send order. See
	// rackState.xmitOrder.
```

## Line 63 — type sentPackets

```
// packets is the send window: packets[0] is sequence number base, and
	// each one after it the next. Acknowledged packets at the front are
	// dropped when a new one is added (trimAcked), so this holds what is
	// outstanding and little else -- libutp's outbuf, which holds
	// cur_window_packets (utp_internal.cpp:1071-1077) -- and indexes by
	// distance from base, which stays right however far the sequence number
	// has wrapped. History: HISTORY.md, "sentPackets.packets".
```

## Line 71 — type sentPackets

```
// fullAcked is what onAck last returned as cumulatively acknowledged.
```

## Line 73 — type sentPackets

```
// spare holds records of acknowledged packets, for the next sent. A
	// record is looked at only while its packet is in the window, so one
	// trimmed from the front is free.
```

## Line 77 — type sentPackets

```
// base is the sequence number of packets[0], or the next to be sent
	// when packets is empty.
```

## Line 80 — type sentPackets

```
// lastAck is the highest sequence number acknowledged in order from the
	// first, among those already dropped from packets; hasAck says whether
	// there is one. See LastAckNum.
```

## Line 87 — type sentPackets

```
// fastResendSeqNum is the lowest sequence number still eligible for fast
	// retransmission. Everything below it has either been acked or been fast
	// retransmitted once already, and only the retransmission timer will send
	// it again.
	//
	// This is libutp's `fast_resend_seq_nr` (utp_internal.cpp:470), advanced
	// past each packet as it is resent (:1603) and forward with the
	// cumulative ack (:2186-2188), and tested before any fast retransmit
	// (:1537, :1560). Without it each acknowledgement that still reported a
	// packet missing would resend it again and halve the window again.
	// History: HISTORY.md, "sentPackets.fastResendSeqNum".
```

## Line 100 — type sentPackets

```
// rack is RFC 8985's loss detection, used in place of the rule above
	// when rack.active. See rack.go.
```

## Line 103 — type sentPackets

```
// transmissions counts every packet sent, first or again; see
	// sentPacket.sentOrder.
```

## Line 116 — func newSentPackets

```
// The number the next packet will take, in both roles. libutp's
		// accepting side does the same (utp_internal.cpp:2988-2989); its
		// initiator does not -- fast_resend_seq_nr stays at 1 when
		// utp_connect randomises seq_nr (:2615, :2768), and for half of all
		// starting numbers never catches up. Not copied; see
		// TestFastRetransmitFromAnyInitialSequenceNumber.
```

## Line 140 — type transmissionForgetter

```
// transmissionForgetter is a controller that can drop its record of a packet
// once the sender is done with it. See defaultController.forgetTransmission.
```

## Line 146 — func (*sentPackets).trimAcked

```
// trimAcked drops the acknowledged packets at the front of the window, and
// the controller's records of them: nothing asks about a packet behind the
// window (Ack, OnLost and MarkAllForResend all stop at it).
```

## Line 179 — func (*sentPackets).SeqNumRange

```
// SeqNumRange is the sequence numbers an acknowledgement may name: the one
// before the window, which acknowledges nothing new, through the last sent.
```

## Line 190 — func (*sentPackets).controllerStats

```
// controllerStats returns a snapshot of the congestion controller's state.
```

## Line 195 — func (*sentPackets).QueueingDelay

```
// QueueingDelay is controllerStats().FilteredQueueingDelay, from the
// controller directly when it offers it.
```

## Line 216 — func (*sentPackets).OnWindowFull

```
// OnWindowFull records that the sender was blocked by the congestion window
// rather than by having nothing to send.
```

## Line 222 — func (*sentPackets).OnPeerDelay

```
// OnPeerDelay passes the delay measured on an inbound packet to the
// controller, which uses it to detect clock drift. See
// defaultController.OnPeerDelay.
```

## Line 229 — func (*sentPackets).OnTick

```
// OnTick lets a time-driven controller advance without an ack.
```

## Line 234 — func (*sentPackets).UnackedCount

```
// UnackedCount is how many packets have been sent and not yet acknowledged --
// libutp's `cur_window_packets`.
```

## Line 247 — func (*sentPackets).LastAckedSeqNum

```
// LastAckedSeqNum is the sequence number just before the oldest packet still
// outstanding -- the number a peer repeats when it is reporting a hole.
//
// libutp computes it inline as `seq_nr - cur_window_packets - 1`
// (utp_internal.cpp:1922). The arithmetic here is the same: UnackedCount is
// `NextSeqNum() - 1 - lastAck` whenever anything has been acknowledged, so
// this recovers lastAck, and before the first acknowledgement it gives the
// number before the first packet sent, which is what libutp's expression
// gives there too.
//
// Wrapping is deliberate and correct: sequence numbers are uint16 and libutp
// masks with ACK_NR_MASK for the same reason.
```

## Line 272 — const maxFastResendsPerAck

```
// maxFastResendsPerAck caps how many packets one ack may fast retransmit.
//
// libutp: "Re-send max 4 packets" (utp_internal.cpp:1605-1606). Without a cap
// a single ack that reveals a burst of losses answers with a burst of
// retransmissions, on a path that has just demonstrated it cannot carry one.
```

## Line 279 — func (*sentPackets).TakeLostPackets

```
// TakeLostPackets returns the packets to fast retransmit now, at most
// maxFastResendsPerAck of them, and advances fastResendSeqNum past each so it
// is not fast retransmitted again. Only the retransmission timer will send
// them a third time.
//
// It mutates, unlike an ordinary getter, because taking a packet and marking
// it taken cannot be separated without reintroducing the duplicate-resend bug
// this exists to prevent.
```

## Line 294 — func (*sentPackets).TakeLostPackets

```
// In window order, oldest first. The set is ordered by raw sequence
	// number, which puts 0 before 65535 and so the newest packets first
	// whenever the window spans a wrap.
```

## Line 299 — func (*sentPackets).TakeLostPackets

```
// Already acked or already fast retransmitted.
```

## Line 325 — func (*sentPackets).OnTransmit

```
// OnTransmit records a packet going out, new or sent again.
//
// The send path sends nothing the window does not allow and numbers packets in
// order, so none of its errors should happen; each says this connection's
// bookkeeping has gone wrong. They panicked, and took the whole process -- every
// other connection, and the application -- with them. The connection now ends
// with the error instead (connection.transmit). libutp asserts the same
// conditions, and its release builds compile the asserts out and carry on.
// The packet is recorded only once nothing has refused it.
```

## Line 347 — func (*sentPackets).OnTransmit

```
// Check for out of order transmit
```

## Line 350 — func (*sentPackets).OnTransmit

```
// Behind the window: a packet already acknowledged and dropped.
			// Nothing to account.
```

## Line 357 — func (*sentPackets).OnTransmit

```
// Check window size for new transmissions
```

## Line 373 — func (*sentPackets).OnTransmit

```
// Update existing packet
```

## Line 377 — func (*sentPackets).OnTransmit

```
// Sent again, so no longer waiting to be: libutp clears need_resend
		// in send_packet (utp_internal.cpp:881).
```

## Line 381 — func (*sentPackets).OnTransmit

```
// Create new packet
```

## Line 402 — func (*sentPackets).onAckOfNothingNew

```
// onAckOfNothingNew is onAck for an acknowledgement of the number just before
// the window with no selective ack: the delay sample, and nothing to retire.
```

## Line 415 — func (*sentPackets).onAck

```
// Once per acknowledgement, whatever it acknowledges: libutp records the
	// sample even from one whose ack number it discards (utp_internal.cpp:
	// 1907, :2023-2024).
```

## Line 422 — func (*sentPackets).onAck

```
// Whatever path the acknowledgement takes below, its window update
	// happens once. detectAndRecordLosses applies it earlier, before a loss
	// can decay the window, as libutp's order has it.
```

## Line 427 — func (*sentPackets).onAck

```
// Check if ack number is in valid range
```

## Line 445 — func (*sentPackets).onAck

```
// ackNum names the sequence number before the first packet we sent,
		// so there is nothing to acknowledge cumulatively. The selective ack
		// still does: it names packets that arrived past the gap, and those
		// are what declare the first packet lost.
		//
		// This branch previously did nothing at all, which meant that when
		// the very first packet of a connection was lost, every selective ack
		// the peer sent about it was discarded -- no packet was ever declared
		// lost, no fast retransmit happened, and recovery waited for the
		// retransmission timeout. libutp processes the extension whatever
		// ack_nr says (utp_internal.cpp:2289).
```

## Line 464 — func (*sentPackets).onAck

```
// Mark all packets up to ackNum as acknowledged
	// Valid until the next acknowledgement, which is as long as anyone uses
	// it: a new one for each was an allocation per acknowledgement.
```

## Line 475 — func (*sentPackets).onAck

```
// Double wrapping addition for uint16
```

## Line 501 — func (*sentPackets).OnAckNum

```
// An ACK for ackNum implicitly ACKs all sequence numbers that precede ackNum
	// Account for any preceding innerMap packets
	//
	// Nothing left unacknowledged is the ordinary outcome of an ack that
	// retires everything in flight, not an error. It used to be returned as
	// one, and processAck then stopped short of everything after the ack
	// itself: the timers of the retired packets stayed armed, the
	// retransmission deadline was not restarted, the consecutive-timeout
	// count was not reset, and an MTU probe's acknowledgement was never seen.
	// On any connection not sending flat out, that was every ack. libutp's
	// ack_packet has no such failure (utp_internal.cpp:1329-1400).
```

## Line 522 — func (*sentPackets).OnAckNum

```
// Advance the fast-retransmit floor with the cumulative ack, as libutp
	// does at utp_internal.cpp:2186-2188.
```

## Line 534 — func (*sentPackets).detectAndRecordLosses

```
// detectAndRecordLosses declares newly lost packets and tells the congestion
// controller about each one. A packet is declared lost once: see
// fastResendSeqNum.
```

## Line 538 — func (*sentPackets).detectAndRecordLosses

```
// libutp's apply_ccontrol (utp_internal.cpp:2139) precedes the decay a
	// selective ack can cause (:2289).
```

## Line 547 — func (*sentPackets).detectAndRecordLosses

```
// Nothing outstanding, so nothing can be lost.
```

## Line 569 — func (*sentPackets).applySelectiveAckBits

```
// applySelectiveAckBits acknowledges the packets named by a selective ack's
// bitfield, without touching ackNum itself.
```

## Line 579 — func (*sentPackets).applySelectiveAckBits

```
// The first bit of the selective ACK corresponds to ackNum + 2,
	// where ackNum + 1 is assumed to have been dropped
```

## Line 584 — func (*sentPackets).applySelectiveAckBits

```
// Break once we exhaust all sent sequence numbers
		// The selective ACK length is a multiple of 32, so it may be padded
```

## Line 596 — func (*sentPackets).applySelectiveAckBits

```
// wrapping addition for uint16
```

## Line 608 — func (*sentPackets).DetectLostPackets

```
// Iterate in reverse order
```

## Line 614 — func (*sentPackets).DetectLostPackets

```
// The fastResendSeqNum test is libutp's, at
			// utp_internal.cpp:1537: a packet already fast retransmitted once
			// is not declared lost again, so the window is not halved again
			// for the same loss.
```

## Line 631 — func (*sentPackets).Ack

```
// Behind the window: already acknowledged and dropped.
```

## Line 635 — func (*sentPackets).Ack

```
// Past what was sent. The connection drops such acknowledgements
		// before they get here (FUZZING.md, "Packets acking data we never
		// sent were processed"); this indexed past the window and panicked
		// if one ever did.
```

## Line 712 — func (*sentPackets).SeqNumIndex

```
// SeqNumIndex is seqNum's position in the window, counting from packets[0].
// A number before the window comes out at 32768 or more.
```

## Line 718 — func (*sentPackets).Outstanding

```
// Outstanding reports whether seqNum names a packet that was sent and has not
// been acknowledged.
//
// It exists for the retransmission timer. Timers are armed per packet and
// cancelled when the ack arrives, but a timer that has already fired is out of
// the wheel and on its way to the event loop, where cancelling it can no
// longer stop it. Acting on such a timeout resends a packet the peer already
// has.
```

## Line 740 — func (*sentPackets).MarkAllForResend

```
// MarkAllForResend gives up every packet still outstanding as lost, as libutp
// does on a retransmission timeout: "every packet should be considered lost"
// (utp_internal.cpp:1230-1237). Each is flagged to be sent again and stops
// counting in flight. Nothing is sent here: the caller resends the oldest,
// and the rest go out oldest first as the window allows -- see
// NextNeedingResend.
```

## Line 761 — func (*sentPackets).NextNeedingResend

```
// NextNeedingResend is the oldest outstanding packet waiting to be sent again
// after a retransmission timeout, if any. libutp's flush_packets walks the
// same packets in the same order (utp_internal.cpp:970-985).
```

## Line 778 — func (*sentPackets).OldestOutstanding

```
// OldestOutstanding is the packet a retransmission timeout resends: libutp's
// `outbuf.get(seq_nr - cur_window_packets)` (utp_internal.cpp:1249).
```

## Line 809 — func (*sentPackets).FirstUnackedSeqNum

```
// wrapping addition for uint16
```
