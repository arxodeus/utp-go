# netem/duplication_test.go

Every comment in `netem/duplication_test.go` at e56c9aa, verbatim.

## Line 9 — func TestDuplicatedPacketsCauseNoRetransmission

```
// A path that delivers some packets twice costs nothing: duplicate data is
// discarded, and a duplicated acknowledgement is not taken for a report of
// loss. go-utp's interop suite runs its transfers over such a link; this
// harness could not make one until Config.DuplicateRate.
//
// Measured over 20 ms and 10 Mbps, 2 MB, three runs each: this library
// sending 7.64-7.69 Mbps with no duplication and 7.63-7.69 with 10% in both
// directions, against libutp's 6.65 and 6.65-7.48. The gate is that the
// transfer verifies and that no packet is resent: nothing is lost here, so
// any retransmission is one a duplicate caused.
```
