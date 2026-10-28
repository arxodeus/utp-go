# delay_set_test.go

Every comment in `delay_set_test.go` at e56c9aa, verbatim.

## Line 9

```
// The retransmission timer's behaviour is the difference between a working
// congestion controller and one being fed phantom loss, so the wheel's timing
// is tested directly rather than inferred from transfer results.
```

## Line 42 — func TestTimeWheelNeverFiresEarly

```
// An item must never fire before its delay. Firing early is what declared
// packets lost before their ack could arrive. "Before" is to the millisecond,
// libutp's resolution (wheelResolution).
```

## Line 51 — func TestTimeWheelNeverFiresEarly

```
// Delays deliberately not multiples of the interval, including one
	// shorter than a single tick.
```

## Line 75 — func TestTimeWheelNeverFiresEarly

```
// The wheel's resolution is one interval, plus scheduler slack.
```

## Line 82 — func TestTimeWheelSubIntervalDelayWaitsATick

```
// A delay shorter than one tick must still wait a tick, not fire on the very
// next one regardless of when it was scheduled.
```

## Line 102 — func TestTimeWheelHandlesDelaysBeyondOneRevolution

```
// Delays longer than one revolution must be scheduled, not clamped. RTO
// backoff depends on this.
```

## Line 106 — func TestTimeWheelHandlesDelaysBeyondOneRevolution

```
// one revolution is 80ms
```

## Line 112 — func TestTimeWheelHandlesDelaysBeyondOneRevolution

```
// 260ms: well past the old ceiling
```

## Line 115 — func TestTimeWheelHandlesDelaysBeyondOneRevolution

```
// Before it is due, it must not have fired.
```

## Line 162 — func TestTimeWheelPutReschedules

```
// Re-putting a key reschedules it rather than leaving two registrations,
// which is what retransmitting an already-armed packet does.
```

## Line 207 — func TestRetransmitKeysAreScoped

```
// The wheel is shared across a socket, so keys from different connections
// must not collide.
```

## Line 231 — func TestRetransmitKeysAreScoped

```
// Disarming one scope must leave the other alone.
```

## Line 238 — func TestTimeWheelNeverFiresEarlyWhenArmedMidCycle

```
// The same promise, but armed part-way through a tick.
//
// TestTimeWheelNeverFiresEarly arms everything immediately after the wheel is
// created, when the next tick is a full interval away. That is the one case
// where placing an item n-1 slots ahead happens to be right, so the wheel
// passed that test while firing up to a full interval early for every timer
// armed at any other moment -- which, in a live connection, is all of them.
//
// This arms at a range of offsets through the tick cycle. It reproduced the
// defect at every offset but zero.
```

## Line 258 — func TestTimeWheelNeverFiresEarlyWhenArmedMidCycle

```
// Let the wheel get part-way through a tick before arming.
```

## Line 279 — func TestTimeWheelNeverFiresEarlyWhenArmedMidCycle

```
// f.at records the time since the recorder was created; the
				// delay is owed from when the item was armed.
```
