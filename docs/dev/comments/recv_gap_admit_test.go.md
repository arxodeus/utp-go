# recv_gap_admit_test.go

Every comment in `recv_gap_admit_test.go` at e56c9aa, verbatim.

## Line 5 — func TestGapLargerThanAnyPacketReceivedIsFilled

```
// The packet that fills a gap is taken even when it is larger than any packet
// received so far. The reserve held for it was the largest packet received,
// and the missing one, never having arrived, can be larger: twelve 300-byte
// packets behind a gap left 496 bytes of a 4,096-byte buffer, a 1,400-byte
// retransmission never fit, nothing was readable, and the connection stalled.
// From the release review of 9c44c4f (R5).
```

## Line 16 — func TestGapLargerThanAnyPacketReceivedIsFilled

```
// refusing out-of-order data to keep room for the gap is fine
```

## Line 32 — func TestPacketLargerThanTheWholeBufferIsTakenWhenEmpty

```
// A buffer smaller than one packet takes the packet when it holds nothing
// readable, rather than refusing it for ever.
```

## Line 44 — func TestInOrderPacketStillWaitsForTheReader

```
// The exception is for a buffer with nothing to read. One with readable data
// still refuses what does not fit: the reader will free room, and the peer
// must wait for it.
```
