# virtual_clock_test.go

Every comment in `virtual_clock_test.go` at e56c9aa, verbatim.

## Line 8 — type virtualClock

```
// virtualClock is a Clock whose time moves only when a test says so.
//
// It exists to take a connection off the real clock entirely. The conformance
// corpus drives real libutp through a driver that is synchronous and has a
// virtual clock, so libutp's emissions happen at exact, known instants. Ours
// happened whenever a goroutine was scheduled, which is why every timing
// comparison in this repository is either coarse, a bound, or absent -- and
// why two harness defects this session were timing assumptions rather than
// library faults.
//
// # The hard part is not the clock, it is knowing when to advance it
//
// Moving time forward is trivial. Knowing that the connection has *finished*
// reacting to the previous instant is not: it runs on its own goroutine, and
// from outside there is no way to tell "parked in select with nothing to do"
// from "about to emit a packet".
//
// So the connection cooperates. Its event loop marks itself idle just before
// it blocks and busy as soon as it wakes, and Advance waits for every
// registered participant to be idle before it moves. That turns "wait 25ms
// and hope" into an actual barrier.
```

## Line 33 — type virtualClock

```
// parks counts how many times a participant has reported that it is
	// about to block, and registered how many participants exist. Advance
	// waits for parks to rise rather than for a state to be true, so a
	// wake-up that is never followed by another park blocks it rather than
	// letting it race ahead.
	// parked counts participants currently blocked and registered how many
	// exist. Advance moves only while they are equal.
```

## Line 42 — type virtualClock

```
// wakes counts every MarkBusy. Delivering into a participant's channel
	// is asynchronous -- the send returns long before the participant runs --
	// so "everything is parked" immediately afterwards is a *stale* answer,
	// and acting on it drops the next fire on a full channel. Waiting for
	// this to move first is how the clock knows the delivery has actually
	// been picked up.
	//
	// Measured before it existed: the retransmission wheel ticks every 25ms,
	// so a two-second advance should fire it eighty times; with the stale
	// check it took delivery of one and dropped seventy-nine, and nothing
	// ever retransmitted.
```

## Line 54 — type virtualClock

```
// inflight counts things handed to a participant that has not yet woken
	// to take them. See idleBarrier.NoteHandoff.
```

## Line 62 — type virtualWaiter

```
// non-zero for a ticker
```

## Line 67 — type virtualWaiter

```
// fn, when set, is run by Advance in place of a send on ch, which is
	// then nil: a timer made by AfterFunc.
```

## Line 88 — func (*virtualClock).AfterFunc

```
// AfterFunc implements funcTimerClock. Advance runs f on its own goroutine at
// the deadline and then waits for the reaction, as it does for a send.
```

## Line 102 — type virtualTicker

```
// virtualTicker adapts a waiter to Ticker, whose Stop returns nothing.
```

## Line 138 — func (*virtualWaiter).Reset

```
// Drain a pending fire, as (*time.Timer).Reset callers expect after Stop.
```

## Line 146 — func (*virtualClock).Register

```
// Register implements idleBarrier.
```

## Line 154 — func (*virtualClock).Unregister

```
// Unregister removes a participant that is going away for good. A goroutine
// that has exited can never park again, and one that never parks would stop
// the clock permanently.
```

## Line 166 — func (*virtualClock).Participants

```
// Participants is how many participants are registered now. A connection's
// event loop unregisters as it ends, inside the step that ended it, which
// makes this the exact moment a connection gave up.
```

## Line 175 — func (*virtualClock).MarkIdle

```
// MarkIdle implements idleBarrier: the caller is about to block.
```

## Line 183 — func (*virtualClock).MarkBusy

```
// MarkBusy implements idleBarrier: the caller has woken.
```

## Line 194 — func (*virtualClock).NoteHandoff

```
// NoteHandoff implements idleBarrier: the caller is about to queue something
// for another participant.
```

## Line 202 — func (*virtualClock).TakeHandoff

```
// TakeHandoff implements idleBarrier: the caller now holds the item, or the
// sender is cancelling a handoff that did not happen.
//
// Deliberately not folded into MarkBusy. A participant wakes for reasons that
// have nothing to do with a handoff -- the wheel wakes on every tick, most of
// which expire nothing -- and letting any wake consume an outstanding handoff
// would clear the count while the item was still queued, which is the state
// this exists to detect.
```

## Line 219 — func (*virtualClock).awaitReaction

```
// awaitReaction blocks until at least one participant has woken since the
// given wake count and everything is parked again.
```

## Line 229 — func (*virtualClock).wakeCount

```
// wakeCount is the number of wake-ups seen so far.
```

## Line 236 — func (*virtualClock).AwaitReactionTo

```
// AwaitReactionTo runs an action that wakes the system -- injecting a packet,
// typically -- and blocks until the reaction to it is complete.
//
// A test's own goroutine is not a participant, so it cannot simply act and
// then ask whether everything is parked: the answer would describe the state
// before the action, and every participant would still be parked from the
// moment before. This captures the wake count first, so the wait cannot be
// satisfied by the past.
```

## Line 250 — func (*virtualClock).AwaitParticipants

```
// AwaitParticipants blocks until at least n participants have registered.
//
// Necessary before the first AwaitQuiet, because "everything registered so
// far is parked" is trivially true when nothing has registered yet. The
// socket's retransmission wheel registers as soon as it is built and a
// connection's event loop only when its goroutine runs, so a test that waited
// for quiet without this would be told the connection was idle before it had
// started -- which it was, and which is not what the question meant.
```

## Line 266 — func (*virtualClock).AwaitQuiet

```
// AwaitQuiet blocks until every registered participant is parked.
//
// A harness calls this after injecting a packet: the injection wakes the
// connection, and this is how it learns the reaction is over. It is the same
// question Advance asks, and the reason neither needs a settle loop.
```

## Line 279 — func (*virtualClock).Advance

```
// Advance moves time forward by d, firing every waiter due along the way and
// letting the participants react to each before moving on.
//
// Firing in deadline order and waiting between matters: a connection that
// reacts to a retransmission timeout by arming another timer must have that
// timer scheduled against the right instant, not against the end of the whole
// advance.
```

## Line 318 — func (*virtualClock).Advance

```
// A function timer reacts by waking a participant -- a resting
			// connection's loop starting again -- and is waited for as a
			// delivered send is.
```

## Line 327 — func (*virtualClock).Advance

```
// Wait for the delivery to be picked up *and* for everything to
			// be parked again before considering the next deadline. Without
			// the first half the answer is stale and the next fire is
			// dropped; without the second, a connection that answers a
			// retransmission timeout by arming another timer would have that
			// timer scheduled against the end of the whole advance rather
			// than against the instant it was armed at.
```

## Line 336 — func (*virtualClock).Advance

```
// A timer channel holds one pending fire, exactly as
			// time.Timer's does; a participant that has not drained the last
			// one is not owed a second, and nothing was delivered to wait
			// for.
```
