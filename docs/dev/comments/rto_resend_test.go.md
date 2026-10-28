# rto_resend_test.go

Every comment in `rto_resend_test.go` at e56c9aa, verbatim.

## Line 10 — func TestRetransmissionTimeoutResendsOnlyTheOldest

```
// A retransmission timeout resends one packet, the oldest, and the rest follow
// as acknowledgements come back.
//
// libutp marks every packet in flight need_resend and resends only the oldest
// (utp_internal.cpp:1230-1252). The rest go out from flush_packets as the
// window allows, or one per acknowledgement through the fast-timeout path
// (:2256-2282). This library used to resend every packet whose own timer
// fired, which on a blacked-out link was the whole window at the timeout and
// most of it again a second later (netem.TestRTOBurstMeasure).
```

## Line 21 — func TestRetransmissionTimeoutResendsOnlyTheOldest

```
// An acknowledgement advertising less than a packet of room leaves
	// processWrites unable to send anything, so only the fast-timeout retry
	// can bring the next packet back -- and libutp's does, because it calls
	// send_packet directly rather than through flush_packets and is_full
	// (utp_internal.cpp:2280-2281). Without this case the fast timeout could
	// be missing and the other case would still pass.
```

## Line 42 — func rtoResendCase

```
// Room for four packets. The opening window is two, and a packet is a
	// full one from the start (the MTU search starts at its ceiling, as
	// libutp's does), so on its own the window would hold two -- not enough
	// for the resend loop below. Set while the connection is parked; the
	// clock's barrier orders it before the loop next runs.
```

## Line 51 — func rtoResendCase

```
// Write more than the window holds, so there are packets in flight and
	// more waiting behind them.
	// The writer is not a participant in the virtual clock, so wait for its
	// packets in real time rather than by advancing the clock.
```

## Line 66 — func rtoResendCase

```
// Say nothing, and wait for the timeout.
```

## Line 83 — func rtoResendCase

```
// Nothing else leaves before the next deadline: the others wait for an
	// acknowledgement or for the next timeout.
```

## Line 93 — func rtoResendCase

```
// The acknowledgement for the oldest brings the next oldest back first,
	// ahead of anything new: fast-timeout retry, then flush_packets in order.
```

## Line 118 — func rtoResendCase

```
// What the window lets out is what was given up at the timeout,
		// oldest first, before any new data: libutp's flush_packets walks its
		// buffer from the oldest. The timeout cut the window to two packets,
		// so not all of it need fit.
```

## Line 159 — type virtualAccepted

```
// virtualAccepted is a connection we accepted, on the virtual clock, with
// the handshake completed by one data packet from the peer.
```

## Line 168 — func newVirtualAccepted

```
// newVirtualAccepted accepts a connection with our sequence number pinned to
// 0x4321, the peer's id 6000 and its first sequence number 900. maxPacket, if
// non-zero, sets MaxPacketSize.
```

## Line 218 — func newVirtualAccepted

```
// Complete the handshake as a peer would, with data that acknowledges our
	// SYN-ACK.
```

## Line 228 — func (*virtualAccepted).writeOne

```
// writeOne writes b and returns the data packets it drew.
```

## Line 237 — func TestAckRetiringEverythingRestartsTheDeadline

```
// An acknowledgement that retires everything in flight restarts the
// retransmission deadline, so the next packet's timeout runs from its own
// send.
//
// It did not. The sent-packet bookkeeping reported "nothing left
// unacknowledged" as an error, and processAck returned before it disarmed the
// retired packets' timers or restarted the deadline. On any connection not
// sending flat out -- request and response, a trickle of protocol messages --
// that was every acknowledgement. The next packet then found timers still
// armed, so it did not start a deadline of its own, and was resent against
// whatever deadline the earlier packet had left: here 3 seconds from the first
// send, where libutp resends one timeout after the second (its ack_packet
// restarts rto_timeout, utp_internal.cpp:1388-1389).
```

## Line 275 — func TestAckRetiringEverythingRestartsTheDeadline

```
// One RTT sample of 100ms puts the timeout at libutp's 1000ms floor.
```
