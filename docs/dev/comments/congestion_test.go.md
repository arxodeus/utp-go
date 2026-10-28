# congestion_test.go

Every comment in `congestion_test.go` at e56c9aa, verbatim.

## Line 14 — func TestOnTransmit

```
// Register the initial transmission of a packet with sequence number 1
```

## Line 31 — func TestOnTransmit

```
// Register the initial transmission of a packet with sequence number 2
```

## Line 48 — func TestOnTransmit

```
// Register the retransmission of the packet with sequence number 2
```

## Line 70 — func TestOnTransmitDuplicateTransmission

```
// Register the initial transmission of a packet with sequence number 1
```

## Line 79 — func TestOnTransmitDuplicateTransmission

```
// Register the initial transmission of the SAME packet
```

## Line 89 — func TestOnTransmitUnknownSeqNum

```
// Register the retransmission of the packet with sequence number 1
```

## Line 99 — func TestOnTransmitInsufficientWindowSize

```
// Register the transmission of a packet with sequence number 1
```

## Line 114 — func TestOnAck

```
// Register the initial transmission
```

## Line 121 — func TestOnAck

```
// Register the acknowledgement
```

## Line 134 — func TestOnAck

```
// Check base delay
```

## Line 137 — func TestOnAck

```
// Check window size
```

## Line 140 — func TestOnAck

```
// Check timeout
```

## Line 144 — func ackOne

```
// ackOne delivers one acknowledgement covering one packet the way
// sentPackets.onAck does: its delay sample, the packet, then the window
// update.
```

## Line 156 — func TestOnAckUnknownSeqNum

```
// Register the acknowledgement for packet with sequence number 1
```

## Line 174 — func TestOnLostPacketRetransmitting

```
// Register initial transmission
```

## Line 183 — func TestOnLostPacketRetransmitting

```
// Register packet loss with retransmission
```

## Line 201 — func TestOnLostPacketUnknownSeqNum

```
// Set initial max window size
```

## Line 205 — func TestOnLostPacketUnknownSeqNum

```
// Try to register loss for unknown sequence number
```

## Line 208 — func TestOnLostPacketUnknownSeqNum

```
// Check error
```

## Line 211 — func TestOnLostPacketUnknownSeqNum

```
// Verify window sizes unchanged
```

## Line 217 — func TestOnTimeoutWithPacketsInFlight

```
// A timeout with packets in flight resets the window to one packet and
// re-enters slow start. libutp: utp_internal.cpp:1223-1228.
```

## Line 238 — func TestOnTimeoutWhileIdleDecaysGently

```
// A timeout on an idle connection -- nothing in flight, so nothing was
// actually lost -- decays the window by a third instead of collapsing it.
//
// libutp: "No need to be aggressive about resetting the congestion window.
// Just let it decay by a 3:rd" (utp_internal.cpp:1216-1222). This fork
// collapsed the window to its floor in both cases, so an application that
// paused long enough to hit an RTO restarted from two packets.
```

## Line 269 — func TestOnTimeoutNotExceedMax

```
// Register timeout
```

## Line 272 — func TestOnTimeoutNotExceedMax

```
// Verify timeout is capped at max
```

## Line 299 — func TestBaseDelay

```
// The smallest sample first, received a window ago and so expired;
	// then three newer ones, in the order acknowledgements arrive.
```

## Line 302 — func TestBaseDelay

```
// Past the window by a margin: exactly a window ago is the boundary,
	// and a clock that has not moved since (Windows' can be that coarse)
	// leaves it in.
```

## Line 317 — func TestBaseDelay

```
// The 50 ms sample is gone already: it can never be the least while the
	// 25 ms one, newer and smaller, is in the window.
```

## Line 321 — func TestBaseDelay

```
// Get base delay
```

## Line 324 — func TestBaseDelay

```
// Check that expired delay was removed
```

## Line 335 — func TestWindowDecaysAtMostOncePerInterval

```
// The congestion window is halved at most once per maxWindowDecayInterval,
// however many packets are declared lost in that span.
//
// libutp: MAX_WINDOW_DECAY, guarded by can_decay_win
// (utp_internal.cpp:51, :602-605). Without the guard a burst of four losses
// -- one queue overflow -- took the window to a sixteenth in a single event.
```

## Line 351 — func TestWindowDecaysAtMostOncePerInterval

```
// Four losses in the same instant.
```

## Line 365 — func TestWindowDecaysAtMostOncePerInterval

```
// Still inside the interval: no further decay.
```

## Line 371 — func TestWindowDecaysAtMostOncePerInterval

```
// Past the interval: decay again.
```

## Line 378 — func TestDelayClampedToRTT

```
// A peer's reported delay cannot drive the window below what the round trip
// says is possible.
//
// libutp clamps the delay it feeds the controller to the minimum round-trip
// time of the packets the acknowledgement covers:
//
//	// the delay can never be greater than the rtt. The min_rtt
//	// variable is the RTT in microseconds
//	int32 our_delay = min<uint32>(our_hist.get_value(), uint32(min_rtt));
//	                                        (utp_internal.cpp:1615-1621)
//
// The delay it is clamping is not measured locally. It arrives in the
// timestamp-difference field of every incoming packet and is passed to the
// controller unaltered (conn_receive.go, onPacket: `delay :=
// time.Duration(replyMicros) * time.Microsecond`), so it is a 32-bit number
// under the remote peer's control. Nothing else between the wire and the
// congestion window checks it.
//
// Without the clamp one packet ends the connection's usefulness. The gain is
// `MAX_CWND_INCREASE_BYTES_PER_RTT * window_factor * (target - our_delay) /
// target`, so a reported delay of 30 seconds against a 100 ms target makes the
// second factor -299 and takes the window to its floor on a single
// acknowledgement -- by malice, or by a peer whose clock stepped, or by a
// timestamp that wrapped.
```

## Line 410 — func TestDelayClampedToRTT

```
// grow drives one honest transmit-and-acknowledge cycle.
```

## Line 432 — func TestDelayClampedToRTT

```
// A peer that lies does not lie once. Twenty acknowledgements is what a
	// second of a hostile or broken peer looks like. (The first version of
	// this test injected exactly one, and before the delay filter and the
	// base shift were in, twenty unclamped took the window to its floor.)
```

## Line 446 — func TestDelayClampedToRTT

```
// Clamped, the delay reads as 20ms against a 100ms target, which is under
	// target, so the window holds or grows.
	//
	// Unclamped it no longer reaches the floor, and a threshold at half the
	// window stopped telling the two apart once libutp's other defence was
	// in: a delay beyond the round trip raises the base by the excess
	// (utp_internal.cpp:2127-2133), so after the three acknowledgements it
	// takes to fill the delay filter the lie reads as 20ms anyway. What is
	// left for the clamp is the acknowledgement that filled it, which
	// unclamped takes about 17% (measured: 84000 -> 69052 bytes). So the
	// test is that the window does not shrink at all.
```

## Line 467 — func TestOneWindowUpdatePerAcknowledgement

```
// An acknowledgement that covers several packets updates the window once, for
// all their bytes: libutp sums acked_bytes over the packets an incoming
// packet acknowledges and calls apply_ccontrol once with the total
// (utp_internal.cpp:1956-1987, :2139-2140). Updating once per packet instead
// compounds -- each later packet sees the window the earlier ones grew -- and
// lands somewhere else.
```

## Line 484 — func TestOneWindowUpdatePerAcknowledgement

```
// not application-limited
```

## Line 485 — func TestOneWindowUpdatePerAcknowledgement

```
// Establish the base delay the samples below are measured against.
```

## Line 514 — func TestOneWindowUpdatePerAcknowledgement

```
// And a second ApplyAck, with nothing acknowledged since, is not a
		// second update.
```

## Line 522 — func TestDelayOverTheRoundTripRaisesTheBase

```
// A one-way delay longer than the round trip cannot be queueing; it is the
// clocks. libutp raises its base by the excess (utp_internal.cpp:2127-2133),
// so the samples that follow read the path as it is.
```

## Line 539 — func TestDelayOverTheRoundTripRaisesTheBase

```
// Three acknowledgements 100ms over the base: the filter reads 100ms,
	// 80ms more than the round trip allows.
```

## Line 546 — func TestDelayOverTheRoundTripRaisesTheBase

```
// The same reports now read as 20ms of queue.
```
