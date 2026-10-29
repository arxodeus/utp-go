# conformance_delay_filter_test.go

Every comment in `conformance_delay_filter_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 11 — func TestConformanceDelayFilter

```
// The delay classic LEDBAT acts on is not the one the latest acknowledgement
// reported. libutp takes the least of the last three queueing-delay samples
// (DelayHist::get_value, utp_internal.cpp:383-391, CUR_DELAY_SIZE 3 at :44),
// each measured against the base as it stood when it arrived (:357-362), so
// one late packet does not move the window and three in a row do.
//
// libutp is driven alone through acknowledgements whose delays jump about,
// and its congestion-control log gives both the delay each one reported
// (actual_delay) and the delay apply_ccontrol used (our_delay). The same
// reported delays go into our controller, and after each the delay it would
// use has to be libutp's.
//
// Every acknowledgement covers one packet and reports a whole number of
// milliseconds over a fixed base, the round trip is longer than any of the
// delays (the window is full, so the queue of unacknowledged packets is
// long), and so libutp's clamp to the round trip (:1621) never decides the
// value and the log's millisecond resolution loses nothing.
```

## Line 29 — func TestConformanceDelayFilter

```
// Spikes of one, two and three acknowledgements, a fall, and a rise.
```

## Line 41 — func TestConformanceDelayFilter

```
// the base-delay window ages samples on the real clock
```

## Line 42 — func TestConformanceDelayFilter

```
// did the filter ever differ from the latest sample?
```
