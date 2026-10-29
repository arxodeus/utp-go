# netem/idle_timeout_test.go

Every comment in `netem/idle_timeout_test.go` at e56c9aa, verbatim.

## Line 15 — func TestQuietConnectionDoesNotTimeOut

```
// A connection that goes quiet must not time out.
//
// Noticed in the trace of TestApplicationLimitedWindowDoesNotGrow: on a link
// with no loss configured, a connection that finished a bulk transfer and then
// trickled small writes took retransmission timeouts, and each one collapsed
// the congestion window to the floor. Nothing was lost, so nothing should have
// timed out.
//
// This is the shape a BitTorrent peer connection spends most of its life in --
// a burst of blocks, then quiet, then a request. If a quiet connection times
// out, every peer pays a window collapse for having been idle.
//
// libutp only acts on the retransmission timeout when there is something
// outstanding to retransmit: `check_timeouts` requires `cur_window_packets > 0`
// before it treats the deadline as a loss (utp_internal.cpp:1147-1155).
```

## Line 35 — func TestQuietConnectionDoesNotTimeOut

```
// A generous queue and no loss: nothing on this link can legitimately
	// cause a retransmission.
```

## Line 98 — func TestQuietConnectionDoesNotTimeOut

```
// Now the quiet phase: one small write every 200ms, each one comfortably
	// acknowledged long before any plausible timeout.
```
