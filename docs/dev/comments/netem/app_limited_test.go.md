# netem/app_limited_test.go

Every comment in `netem/app_limited_test.go` at e56c9aa, verbatim.

## Line 13 — func TestApplicationLimitedWindowDoesNotGrow

```
// The application-limited guard, measured rather than read.
//
// libutp refuses to grow the congestion window when the application has not
// been filling it:
//
//	if (scaled_gain > 0 && ctx->current_ms - last_maxed_out_window > 1000) {
//	    // if it was more than 1 second since we tried to send a packet
//	    // and stopped because we hit the max window, we're most likely rate
//	    // limited (which prevents us from ever hitting the window size)
//	    // if this is the case, we cannot let the max_window grow indefinitely
//	    scaled_gain = 0;
//	}
//	                                        (utp_internal.cpp:1681-1686)
//
// `last_maxed_out_window` is stamped by `is_full` every time the sender has
// something to send and no window to send it in (:945, :957). The reason the
// guard exists: a window grown while nothing is filling it is a measurement of
// nothing, and the sender dumps it all at once the moment the application
// speeds up.
//
// This library has the same rule (congestion.go, lastMaxedOutWindow) and has
// had it only as a citation. This measures it.
//
// One detail decides whether the test measures anything at all. The guard
// zeroes `scaled_gain`, which is the LEDBAT term; in slow start libutp takes
// `max_window = max(ss_cwnd, ledbat_cwnd)` (:1699), and `ss_cwnd` is not
// gated. So a connection still in slow start grows whatever the application
// does, in both implementations, and a test that goes idle before slow start
// ends would see growth and call it a defect. Phase A exists to end slow
// start before the measurement begins.
```

## Line 74 — func TestApplicationLimitedWindowDoesNotGrow

```
// The receiver reads continuously, so the peer's advertised window never
	// becomes what limits the sender -- the point is to observe congestion
	// control, not flow control.
```

## Line 99 — func TestApplicationLimitedWindowDoesNotGrow

```
// --- phase A: fill the pipe, so slow start is over and the window is
	// large enough that holding it flat means something.
```

## Line 103 — func TestApplicationLimitedWindowDoesNotGrow

```
// Repeated until slow start is actually over, rather than once and hope.
	// Slow start ends on a loss or on the delay crossing 0.9 of target
	// (utp_internal.cpp:1692-1697), and on a link this clean one 512 KB
	// transfer does not reliably produce either: one run in four left the
	// connection still in slow start, where the window grows whatever the
	// application does and the guard cannot be measured at all.
```

## Line 128 — func TestApplicationLimitedWindowDoesNotGrow

```
// --- phase B: application-limited. Small writes, far apart: the sender
	// always has window to spare, so is_full never fires and libutp's guard
	// would hold the window where phase A left it.
```

## Line 153 — func TestApplicationLimitedWindowDoesNotGrow

```
// The measurement starts where the guard does, not where phase B does.
	// libutp suppresses growth only once it has been more than a second since
	// the window was last filled (utp_internal.cpp:1681), so the first second
	// after a phase of hard sending is allowed to grow -- in both
	// implementations. Measuring from the start of phase B counted that
	// second's legitimate growth as a failure, on two runs in three: 16724 to
	// 23031 bytes, all of it before the guard's precondition held.
```

## Line 169 — func TestApplicationLimitedWindowDoesNotGrow

```
// Three things have to be true before the measurement means anything, and
	// each of them was false at some point while this test was being written.
	//
	// The window has to have grown in phase A, or holding it flat afterwards
	// is not evidence of anything.
```

## Line 178 — func TestApplicationLimitedWindowDoesNotGrow

```
// Slow start has to be over. Its term is ungated in both implementations
	// (utp_internal.cpp:1699), so a connection still in slow start grows
	// whatever the application does -- measured here at ~112 bytes per 83ms
	// before the link was given a queue small enough to end slow start with a
	// loss.
```

## Line 188 — func TestApplicationLimitedWindowDoesNotGrow

```
// And the guard's own precondition has to hold: libutp suppresses growth
	// only once it has been more than a second since the window was last
	// filled.
```

## Line 200 — func TestApplicationLimitedWindowDoesNotGrow

```
// The measurement. The window is expected to be flat, not merely bounded:
	// the guard zeroes the gain outright.
```

## Line 228 — func cwndRange

```
// cwndRange reports the first and the largest congestion window observed in
// (after, until].
```

## Line 257 — func waitForIdle

```
// waitForIdle blocks until the connection has nothing buffered and nothing in
// flight, and reports when that happened.
// waitForIdle blocks until the connection has nothing buffered and nothing in
// flight, counting only samples taken after since, and reports when that
// happened.
//
// The `since` argument is not decoration. Without it this returned on the
// stale pre-transfer sample -- pending, in-flight and queued writes all
// legitimately zero before anything had been written -- so phase A ended
// before it began and the window it was supposed to grow never appeared.
```

## Line 293 — func inSlowStart

```
// inSlowStart reports whether any sample in (after, until] was still in slow
// start.
```

## Line 307 — func maxAppLimited

```
// maxAppLimited reports the longest the sender went without filling its window
// during (after, until].
```

## Line 322 — func firstGuarded

```
// firstGuarded reports when the application-limited guard's precondition first
// held: the first sample in (after, until] where the sender had gone more than
// a second without filling its window.
```
