# delay_set.go

Every comment in `delay_set.go` at e56c9aa, verbatim.

## Line 13 — type timeWheelItem

```
// rounds is how many further full revolutions of the wheel must pass
	// before this item expires. It is what lets the wheel schedule delays
	// longer than one revolution instead of silently clamping them.
```

## Line 19 — type timeWheel

```
// timeWheel is a hashed timing wheel. An item fires on the first tick at or
// after its deadline: never early, and less than one interval late. A rounds
// counter lets a delay exceed slotNum*interval, so exponential backoff is not
// clamped.
//
// Removal is O(1): an index maps each key to the slot holding it, which a
// wheel shared across a whole socket needs.
//
// It is generic in its key as well as its value, and keeps items by value,
// so that scheduling one costs no allocation. History: HISTORY.md,
// "timeWheel".
```

## Line 37 — type timeWheel

```
// nextTick is when the tick that processes slots[current] is due: the
	// last tick's scheduled time plus one interval. A ticker keeps its phase,
	// so the next tick is never sooner than this, and later only if one was
	// dropped. See put.
```

## Line 54 — func newTimeWheelWithClock

```
// newTimeWheelWithClock is the wheel on a given clock. The wheel is per
// socket rather than per connection, so every connection on a socket shares
// this one's notion of time -- which is what makes a virtual clock for a
// socket coherent rather than per-connection and contradictory.
```

## Line 77 — func newTimeWheelWithClock

```
// Read before the ticker exists, so that the first tick is no sooner than
	// this says.
```

## Line 81 — func newTimeWheelWithClock

```
// A virtual clock may not move while this goroutine is processing a tick.
	// Most ticks expire nothing and so never reach a connection, which is
	// exactly why the wheel has to report for itself: a clock that fired this
	// ticker and then waited for a *connection* to react would wait forever.
```

## Line 93 — const wheelResolution

```
// wheelResolution is how precisely a deadline is honoured: libutp's
// millisecond. See put.
```

## Line 97 — func (*timeWheel[...]).put

```
// put schedules value to expire after delay. Re-putting an existing key
// reschedules it.
//
// The item goes in the slot whose tick is the first due at or after the
// deadline, to the millisecond, so it never fires early and fires less than
// one interval late. To the millisecond is libutp's resolution: its clock is
// current_ms, a deadline is current_ms plus the timeout, and it fires once
// (int)(current_ms - rto_timeout) >= 0 (utp_internal.cpp:997, :1147, :1389),
// which can be up to a millisecond before the microsecond the deadline was
// set for. A retransmission re-arms its timer from the tick that fired it,
// and placing by the millisecond keeps each backoff on libutp's schedule
// rather than a tick later than the last. History: HISTORY.md,
// "timeWheel.put".
```

## Line 119 — func (*timeWheel[...]).put

```
// Slot current+n is processed by a tick due at nextTick + n*interval or
	// later, so the least n that reaches the deadline is safe.
```

## Line 146 — func (*timeWheel[...]).contains

```
// contains reports whether key is currently scheduled.
```

## Line 165 — func (*timeWheel[...]).run

```
// Collect the expired items under the lock, then dispatch them
			// with the lock released. handleExpireFunc blocks (it hands the
			// packet to a connection's event loop over a channel), and that
			// same event loop calls put/remove, which need this lock.
			// Running the callback under the lock deadlocks the connection as
			// soon as the timeout channel fills up.
```

## Line 192 — func (*timeWheel[...]).run

```
// Deliberately not marking busy: this goroutine is leaving, and a
			// participant that never parks again would block the clock
			// forever. Unregister instead.
```

## Line 211 — func (*timeWheel[...]).stop

```
// stop halts the wheel. It is safe to call more than once: Close on the
// owning socket is expected to be idempotent, and a second close of the
// stopped channel would panic.
```
