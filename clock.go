package utp_go

import "time"

// Clock is where a connection reads time and gets its timers.
//
// It exists so a test can run a connection on a clock it controls. The
// conformance corpus drives real libutp through a driver whose clock is
// virtual and whose execution is synchronous, so libutp's emissions happen at
// exact, known instants; ours happened whenever a goroutine was scheduled,
// which is why the corpus compares what a packet contained and never when it
// was sent. Closing that needs the timers on the same clock as the
// timestamps, not just the timestamps.
//
// ConnectionConfig.NowMicros already made the *wall clock* injectable -- the
// microseconds stamped into packets. This is the other half: the deadlines
// and intervals that decide when anything happens at all.
//
// The real implementation is the package default and wraps the standard
// library one for one. Nothing outside a test should need to set this.
type Clock interface {
	// Now is the current time, used for every deadline comparison.
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) Timer
	// NewTicker returns a ticker that fires every d.
	NewTicker(d time.Duration) Ticker
}

// Timer is the subset of *time.Timer this package uses.
//
// C is a method rather than a field because an interface cannot carry one;
// every call site reads `<-t.C()` where it read `<-t.C`.
type Timer interface {
	C() <-chan time.Time
	Reset(d time.Duration) bool
	Stop() bool
}

// Ticker is the subset of *time.Ticker this package uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// IdleBarrier is implemented by a Clock that needs to know when the
// connection has finished reacting, so that it can advance time safely.
//
// Advancing a virtual clock is trivial; knowing that the connection has
// *finished* with the previous instant is not. It runs on its own goroutine,
// and from outside there is no way to distinguish "parked with nothing to do"
// from "about to emit a packet".
//
// So the event loop reports each time it is about to block, and the clock
// counts those reports. After delivering a timer it waits for the count to
// rise: the loop it just woke must come all the way round and park again
// before time moves further.
//
// **One call site each, deliberately.** The obvious design marks busy in
// every case body of the loop's select, and that select has ten cases -- miss
// one and the clock advances while the loop is working, which is precisely
// the nondeterminism this exists to remove. So the select was restructured to
// receive without acting: it records which case fired, the barrier is marked
// busy once, and the bodies run from a switch afterwards. The invariant is
// then structural rather than a rule to remember.
//
// A counter of parks alone is not enough, which is worth recording because it
// was tried: a clock that fires a timer and then waits for the *connection*
// to park deadlocks the moment a timer belongs to something else. The
// retransmission wheel ticks on its own goroutine and most of its ticks
// expire nothing, so they wake the wheel and never reach a connection at all.
// Both participants report both edges.
//
// The real clock does not implement this, and the cost there is a nil check
// per pass against an interface resolved once when the loop starts.
type IdleBarrier interface {
	// Register announces a participant whose parking matters. Called once,
	// when the loop starts.
	Register()
	// MarkIdle reports that the caller is about to block, and MarkBusy that
	// it has woken. A clock may only move time while every registered
	// participant is idle.
	MarkIdle()
	MarkBusy()
	// NoteHandoff reports that something is being queued for another
	// participant, and TakeHandoff that it has been picked up. Called by the
	// sender before the send and by the receiver once it holds the item, so
	// that every outstanding handoff is accounted for by exactly one of them.
	//
	// Without it "every participant is parked" is not the same as "nothing is
	// in flight". A channel send returns long before the receiver runs, so a
	// participant can hand a packet to another and park, leaving the system
	// momentarily quiet by every observable measure while work is still
	// outstanding. Time would then move before the receiver had reacted.
	//
	// Measured before it existed: the retransmission wheel delivers a timeout
	// and parks, the clock judged the system quiet, and the connection's
	// re-arm landed a tick or two later than it should -- giving a
	// retransmission at 9.05s under one scheduler and 9.075s under another
	// for the same virtual deadline.
	NoteHandoff()
	TakeHandoff()
}

// realClock is the standard library, and the default for every connection.
type realClock struct{}

// RealClock is the clock a connection uses unless one is configured.
var RealClock Clock = realClock{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

func (realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }
