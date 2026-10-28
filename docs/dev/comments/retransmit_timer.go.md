# retransmit_timer.go

Every comment in `retransmit_timer.go` at e56c9aa, verbatim.

## Line 10 — const defaultRetransmitTickInterval, defaultRetransmitSlots

```
// defaultRetransmitTickInterval is the resolution of the retransmission
	// timer wheel: how late a retransmission can be. A timer is never early
	// (timeWheel.put).
	//
	// 25 ms is fine enough that quantisation is negligible against any
	// realistic RTO. It is affordable only because the wheel is shared across
	// the whole socket: one ticker, not one per connection. History:
	// HISTORY.md, "defaultRetransmitTickInterval".
```

## Line 20 — const defaultRetransmitTickInterval, defaultRetransmitSlots

```
// defaultRetransmitSlots gives one revolution of 1.6s at the default
	// interval. Longer delays are handled by the wheel's rounds counter, so
	// this is a distribution parameter, not a ceiling.
```

## Line 26 — type retransmitKey

```
// retransmitKey identifies one outstanding packet inside a socket-wide timer
// wheel. Connections share the wheel, so sequence numbers alone would collide.
```

## Line 33 — type retransmitTimer

```
// retransmitTimer is what the shared wheel stores: the packet to resend and
// where to deliver the expiry.
```

## Line 39 — type retransmitTimer

```
// wake starts the connection's loop if it is resting (dormant.go): a
	// timeout delivered to a loop that has gone would wait for ever.
```

## Line 44 — type retransmitTimers

```
// retransmitTimers is a socket-wide retransmission timer wheel.
//
// One wheel per socket rather than one per connection. A 25 ms interval with
// a wheel per connection meant 40 timer wake-ups per second per connection --
// 40,000/s at a thousand connections, which cost about 25% of throughput.
// Shared, the tick rate is constant regardless of connection count and the
// per-tick work is proportional to the number of timers actually expiring.
```

## Line 60 — func newRetransmitTimersWithClock

```
// newRetransmitTimersWithClock is the socket-wide retransmission wheel on a
// given clock. See newTimeWheelWithClock.
```

## Line 66 — func newRetransmitTimersWithClock

```
// The connection has not taken this yet. See idleBarrier.NoteHandoff.
```

## Line 83 — func newRetransmitTimersWithClock

```
// This connection's event loop is behind. Re-arm for the next
			// tick rather than blocking every other connection's timers
			// behind it, or dropping the timeout and stalling this
			// connection permanently.
```

## Line 93 — func (*retransmitTimers).newScope

```
// newScope returns an identifier unique to one connection on this socket.
```

## Line 104 — func (*retransmitTimers).Len

```
// Len reports how many timers are armed across the whole socket.
```
