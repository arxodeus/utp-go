# netem/two_way_test.go

Every comment in `netem/two_way_test.go` at e56c9aa, verbatim.

## Line 14 — func TestTwoWayTransferOverLoss

```
// Data both ways at once over a lossy path.
//
// Nothing else in the suite sends in both directions on one connection, and
// that is where the peer's acknowledgements stop arriving as ST_STATE: they
// ride on its data packets instead. The writer used to wake only for an
// ST_STATE, so in about one run in five one direction sat with a megabyte
// queued, nothing in flight and room in its window, until a timer happened to
// fire some 30 seconds later. libutp wakes on any packet (utp_internal.cpp:
// 2302-2308). With eight runs, a one-in-five stall shows in about five
// attempts out of six.
```

## Line 31 — func TestTwoWayTransferOverLoss

```
// the median is about 5 s; the stall was about 36
```

## Line 50 — func twoWayTransfer

```
// twoWayTransfer opens one connection and has both ends write size bytes and
// read the other's size bytes at the same time.
```
