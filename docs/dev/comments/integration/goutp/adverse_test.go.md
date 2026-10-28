# integration/goutp/adverse_test.go

Every comment in `integration/goutp/adverse_test.go` at e56c9aa, verbatim.

## Line 13 — func netemPair

```
// netemPair is a pair on an emulated network whose link, both ways, is cfg:
// ours on a netem endpoint directly, purego through netemPacketConn.
```

## Line 34 — func TestPuregoTransferUnderAdverseConditions

```
// Transfers complete intact over links that lose, delay, reorder and
// duplicate packets, in every combination of who dials and who sends. These
// are the conditions under which the two engines' differences show: purego
// reads a selective ack one bit short of where libutp reads it, so a
// disagreement about which packets arrived would leave one of them waiting
// for a retransmission that never comes, and the transfer would not finish.
```
