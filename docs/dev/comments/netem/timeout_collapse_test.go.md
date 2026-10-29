# netem/timeout_collapse_test.go

Every comment in `netem/timeout_collapse_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 17 — func TestTimeoutWithDataInFlightCollapsesWindow

```
// What a retransmission timeout with data in flight does to the window.
//
// This is the other half of libutp's timeout branch. The idle half -- decay by
// a third when nothing is outstanding -- is measured by
// TestLibutpIdleWindowDecay. This is what happens when something *is*:
//
//	} else {
//	    // our delay was so high that our congestion window
//	    // was shrunk below one packet, preventing us from
//	    // sending anything for one time-out period. Now, reset
//	    // the congestion window to fit one packet, to start over
//	    // again
//	    max_window = packet_size;
//	    slow_start = true;
//	}
//	                                        (utp_internal.cpp:1223-1228)
//
// One packet, and back into slow start. Against the idle branch's two thirds
// that is a difference of more than an order of magnitude, which is what makes
// it measurable from outside without reading anyone's window.
//
// Two measurements, doing different jobs, and it is worth being precise about
// which does which.
//
// Our side is read directly: the window and the slow-start flag after the
// timeout. That is the assertion with teeth -- forcing the idle branch instead
// leaves the window at 25569 bytes with slow start off, and both assertions
// fail.
//
// Both sides are also measured by one shared observable, at the receiver: how
// many bytes arrive in the quarter second after recovery starts. That is what
// makes this a comparison against the reference rather than against a reading
// of its source, and the two land within 1.5% of each other -- libutp 43560
// bytes against our 44233, where slow start from one packet doubling over the
// ~5.7 round trips in 250 ms predicts 43.4 KB.
//
// But it is a weak discriminator, and pretending otherwise would be the
// mistake this file is meant to avoid. Under the forced idle branch it read
// 33091 against libutp's 43560 -- *lower*, and well within any tolerance worth
// setting, because a 25 KB window outside slow start ramps more slowly over
// 250 ms than a one-packet window inside it. The agreement is evidence; the
// direct assertions are the test.
```

## Line 70 — func TestTimeoutWithDataInFlightCollapsesWindow

```
// Our side can be checked directly, and is the sharper assertion: libutp
	// sets max_window to one packet and slow_start to true, and both are
	// visible here.
```

## Line 86 — func TestTimeoutWithDataInFlightCollapsesWindow

```
// And libutp has to behave the same way, or the assertions above are
	// pinning our behaviour to a reading of the source rather than to the
	// reference. 60 KB in 250 ms is well above any one-packet ramp and well
	// below what an intact window would deliver.
```

## Line 95 — func TestTimeoutWithDataInFlightCollapsesWindow

```
// The two should land within a factor of three of each other. Loose on
	// purpose: this bound is a sanity check on the comparison, not the thing
	// that catches a wrong branch -- see the note above on why this observable
	// cannot do that job.
```

## Line 106 — func libutpRecoveryAfterBlackout

```
// libutpRecoveryAfterBlackout runs real libutp as the sender, blackholes the
// link mid-transfer for long enough to force a timeout with data in flight,
// and reports how many bytes reach the receiver in the 250 ms after the link
// returns.
```

## Line 200 — func libutpRecoveryAfterBlackout

```
// Let the window grow, with the transfer still running.
```

## Line 209 — func ourRecoveryAfterBlackout

```
// ourRecoveryAfterBlackout does the same with this library as the sender, and
// additionally reports the window, the slow-start flag and the timeout count
// straight after the timeout, which can be read directly on this side.
```

## Line 282 — func ourRecoveryAfterBlackout

```
// The window straight after the timeout: the first sample taken once the
	// blackout ended, before the ramp has had time to move it.
```

## Line 298 — func blackoutAndMeasure

```
// blackoutAndMeasure drops everything on the a->b link for the given duration,
// then reports how many further bytes the receiver takes in the next 250 ms.
```

## Line 315 — func blackoutAndMeasure

```
// Measure from when recovery actually starts, not from when the link
	// returns. Those are not the same instant: the retransmission timeout
	// doubles on each expiry, so after a 1.6s blackout the next attempt is not
	// due for another second and a half. Measuring the 250ms after the link
	// came back read zero bytes for both implementations -- a comparison of
	// two zeroes, which passes and says nothing.
```
