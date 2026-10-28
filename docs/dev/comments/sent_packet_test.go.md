# sent_packet_test.go

Every comment in `sent_packet_test.go` at e56c9aa, verbatim.

## Line 57 — func TestOnTransmitRetransmit

```
// Distinct by construction: two readings of the clock in a row can be
	// equal, as they were on Windows CI.
```

## Line 93 — func TestOnTransmitOutOfOrder

```
// wrapping addition for uint16
```

## Line 106 — func TestOnSelectiveAck

```
// Assuming this value
```

## Line 117 — func TestOnSelectiveAck

```
// Send COUNT packets
```

## Line 124 — func TestOnSelectiveAck

```
// Create selective ACK
```

## Line 133 — func TestOnSelectiveAck

```
// Process ACK
```

## Line 140 — func TestOnSelectiveAck

```
// Verify ACKs
```

## Line 168 — func TestDetectLostPackets

```
// Send packets and selectively acknowledge them
```

## Line 180 — func TestDetectLostPackets

```
// Detect lost packets
```

## Line 185 — func TestDetectLostPackets

```
// Verify lost packets
```

## Line 212 — func TestAck

```
// Artificially insert packet into lost packets
```

## Line 215 — func TestAck

```
// Acknowledge the packet
```

## Line 251 — func TestAckPriorUnacked

```
// Send COUNT packets
```

## Line 258 — func TestAckPriorUnacked

```
// Verify test preconditions
```

## Line 266 — func TestAckPriorUnacked

```
// Acknowledge prior innerMap packets
```

## Line 273 — func TestAckPriorUnacked

```
// Verify acknowledgments
```

## Line 289 — func TestAckUnsent

```
// wrapping addition for uint16
```

## Line 306 — func TestSeqNumIndex

```
// wrapping addition for uint16
```

## Line 312 — func TestFastRetransmitHappensOncePerPacket

```
// A packet is fast retransmitted once. Every further ack that still shows it
// missing leaves it alone; only the retransmission timer sends it again.
//
// libutp advances fast_resend_seq_nr past each packet as it resends it
// (utp_internal.cpp:1603) and refuses to fast resend anything below it
// (:1537). Without that, a packet declared lost was resent on every ack until
// its own ack came back: measured on a 2%-loss link, 52 fast retransmissions
// for 7 distinct lost packets, one packet sent 16 extra times.
```

## Line 330 — func TestFastRetransmitHappensOncePerPacket

```
// 101 is missing; 102 through 105 arrive. That is four acks past the
	// gap, so 101 is declared lost.
```

## Line 346 — func TestFastRetransmitHappensOncePerPacket

```
// The same ack again, and again. 101 is still missing, and must not be
	// resent.
```

## Line 359 — func TestFastRetransmitFromAnyInitialSequenceNumber

```
// Fast retransmission works whatever the first sequence number is.
//
// A libutp initiator gets this wrong. utp_create_socket sets seq_nr = 1 and
// fast_resend_seq_nr = seq_nr (utp_internal.cpp:2611-2615); utp_connect then
// replaces seq_nr with a random number (:2768) and leaves fast_resend_seq_nr
// at 1. Acknowledgements advance it only through a wrapping less-than
// (:2186-2188), and when the random start is more than half the sequence
// space past 1, 1 counts as ahead and it never moves -- so the connection
// cannot fast-retransmit (:1537, :1560) until the numbers wrap round. The
// accepting side sets it after choosing seq_nr (:2988-2989) and is correct.
// Measured through the real-socket relay: every one of 61 stalls in which
// libutp had the selective-ack evidence and did not resend came from such a
// connection, and none from the other half (KNOWN-LIMITATIONS.md).
//
// This library starts fast_resend at the first sequence number in both roles,
// which is what the accepting side of libutp does.
```

## Line 383 — func TestFastRetransmitFromAnyInitialSequenceNumber

```
// The first packet is missing; the next four arrive.
```

## Line 396 — func TestFastRetransmitIsCappedPerAck

```
// One ack fast retransmits at most four packets, however many it reveals as
// lost. libutp: "Re-send max 4 packets" (utp_internal.cpp:1605-1606).
```

## Line 409 — func TestFastRetransmitIsCappedPerAck

```
// Ten missing packets with acked packets above all of them: 102, 104,
	// 106 ... are acked, the odd ones are not.
```

## Line 431 — func TestFastRetransmitIsCappedPerAck

```
// The rest are still pending, and come out on the next round rather than
	// being forgotten.
```
