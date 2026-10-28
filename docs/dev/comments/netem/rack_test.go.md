# netem/rack_test.go

Every comment in `netem/rack_test.go` at e56c9aa, verbatim.

## Line 11 — func rackRetransmissions

```
// rackRetransmissions runs one transfer over a reordering link and returns
// how many packets the sender resent.
```

## Line 39 — func TestRackResendsLessUnderReordering

```
// A packet displaced by 5 ms on a 40 ms round trip is reordered, not lost.
// libutp's rule resends it once three later packets arrive first; RACK
// waits a quarter of the minimum round trip, 10 ms here, once the path has
// been seen to reorder. Nothing is lost on this link, so every resend is
// spurious. Measured over 2 MB: 23-24 resends with libutp's rule, 3 with
// RACK, three runs each.
```
