# netem/window_decay_test.go

Every comment in `netem/window_decay_test.go` at e56c9aa, verbatim.

## Line 13 — func TestWindowDecayIsRateLimited

```
// One burst of loss must halve the window once, not once per lost packet.
//
// libutp rate-limits the decay:
//
//	bool can_decay_win(int64 msec) const {
//	    return (msec - last_rwin_decay) >= MAX_WINDOW_DECAY;   // 100 ms
//	}
//	void maybe_decay_win(uint64 current_ms) {
//	    if (can_decay_win(current_ms)) {
//	        max_window = (size_t)(max_window * .5);
//	        last_rwin_decay = current_ms;
//	        ...
//	                    (utp_internal.cpp:602-615, the constant at :51)
//
// and calls it once per acknowledgement that resent anything (`:1610`), not
// once per packet resent. So a queue overflow that loses a dozen packets in
// one window costs one halving, and nothing more for 100 ms however many acks
// report the hole.
//
// This library has the same rule and has had it only as a citation. The
// failure it prevents is not subtle: halving per lost packet takes a window to
// a sixteenth on four losses, and to the floor on eight, after which the
// connection crawls.
//
// The loss is staged rather than waited for -- the link is told to drop half of
// what crosses it for 25 ms and then to stop -- so the burst lands inside one
// window and is recovered by duplicate acknowledgements rather than by a
// timeout. That distinction matters: a retransmission timeout collapses the
// window to one packet through a different branch entirely
// (utp_internal.cpp:1223-1228), which would mask whatever the decay limiter
// did. The test asserts no timeout happened rather than assuming it.
```

## Line 106 — func TestWindowDecayIsRateLimited

```
// Let the window grow before damaging anything.
```

## Line 114 — func TestWindowDecayIsRateLimited

```
// Half the packets, not all of them.
	//
	// Dropping everything for 25ms was the first attempt and recovered
	// nothing: with no packet arriving after the gap the receiver has nothing
	// to acknowledge, so there are no duplicate acknowledgements, no fast
	// retransmit, and recovery waits for the retransmission timeout -- the
	// one branch this test has to avoid. Losing half of a burst leaves plenty
	// arriving to report the holes.
```

## Line 132 — func TestWindowDecayIsRateLimited

```
// Long enough for the hole to be reported and recovered, short enough to
	// stay well inside the one-second retransmission timeout.
```

## Line 146 — func TestWindowDecayIsRateLimited

```
// Preconditions, each of which was worth asserting rather than assuming.
```

## Line 173 — func TestWindowDecayIsRateLimited

```
// The measurement. One halving is 50%; two would be 25%. The bound sits
	// between them, low enough to allow a second decay 100ms later during a
	// long recovery and still fail the per-packet halving this exists to
	// prevent, which reaches a sixteenth on four losses.
	// The measurement. One halving leaves 50%, two leave 25%; recovery here
	// takes one or two round trips, so one or two decays are both within the
	// rule. The bound sits below two and far above what halving per lost
	// packet would produce: ten losses would take the window to a thousandth
	// of itself, which is the floor.
```

## Line 198 — func lowestCwndAfter

```
// lowestCwndAfter reports the smallest congestion window observed at or after
// the given moment.
```
