# netem/libutp_congestion_test.go

Every comment in `netem/libutp_congestion_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 11 — func TestCongestionRespondsToSelfInflictedQueue

```
// How each implementation's congestion control responds to a queue it caused.
//
// Everything comparing this library to libutp so far has compared *goodput*:
// how fast a transfer finished on five links. That is a coarse instrument, and
// COMPATIBILITY.md says so -- two controllers can reach the same throughput by
// different routes, and every mechanism inside the controller is still recorded
// there as *cited*, matched by reading utp_internal.cpp rather than by running
// it.
//
// This measures the mechanism that defines LEDBAT: what it does about queueing
// delay of its own making. The bottleneck is cut to 40% of its rate part-way
// through a transfer, so a sender already filling the old capacity is suddenly
// overdriving the new one. A queue builds. A delay-based controller is supposed
// to notice that in the one-way delay and back off until the standing queue is
// small; a loss-based one would keep pushing until the queue overflowed.
//
// The measurement is the queueing delay the link actually reports, which is the
// quantity LEDBAT exists to control, and it is measured the same way for both
// implementations.
//
// An earlier version of this test stepped the *propagation* delay instead and
// asserted the sending rate fell. Both implementations passed it, and it was
// measuring nothing: tripling the RTT cuts the rate of any fixed window
// mechanically, so the assertion held whatever the controller did. Working the
// rates back into windows showed both had in fact *grown* their window across
// the step -- correctly, because a permanent propagation change is not
// queueing, and LEDBAT relearns it as the new base delay. The test was
// confounded in the direction that made it pass.
//
// Reading the window directly would be better and is not available: libutp
// keeps max_window private to UTPSocket, utp_socket_stats does not report it,
// and reaching in would mean modifying the vendored copy REFERENCE.md pins.
//
// What this does not establish: that the two controllers agree on how *much*
// queue to leave. They demonstrably do not -- BENCHMARKS.md records this fork's
// classic LEDBAT leaving 33ms of standing queue on a 40ms path -- so the
// numbers are reported for comparison and the assertion is only that each
// keeps the queue bounded rather than filling it until it overflows.
```

## Line 88 — func TestCongestionRespondsToSelfInflictedQueue

```
// Let it settle at the full rate, then read the queue it is holding.
```

## Line 98 — func TestCongestionRespondsToSelfInflictedQueue

```
// Reset so the delay measured afterwards is only the new regime's.
```

## Line 122 — func TestCongestionRespondsToSelfInflictedQueue

```
// The queue is 256KB against a 4 Mbps bottleneck: filling it is half a
		// second of delay. A controller reading the delay signal settles well
		// short of that. One that ignored it would sit at the drop tail.
```

## Line 137 — func TestCongestionSlowStartRamp

```
// How fast each implementation ramps up from a standing start.
//
// Slow start is recorded in COMPATIBILITY.md as *cited* -- and it was absent
// from this fork entirely until M5, so it is worth knowing that what replaced
// nothing behaves like the reference rather than merely existing. The shape is
// the claim: slow start doubles the window each round trip, so a sender
// reaches a bottleneck in a time that grows with the logarithm of the window,
// not linearly.
//
// Measured as the time to first carry 90% of the link rate in a sampling
// interval, on an idle path with no loss, which is the condition slow start is
// for. Both numbers are reported; the assertion is only that neither takes
// absurdly long, because the two do not have to agree exactly -- this fork's
// slow start is gain-scaled per the LEDBAT++ draft when that algorithm is
// selected, and libutp has no such notion.
```
