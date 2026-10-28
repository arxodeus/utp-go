package utp

import "time"

// Clock is the source of time and timers for a connection. It exists so
// tests can run connections on a virtual clock; the default, RealClock, uses
// the time package. ConnectionConfig.NowMicros controls the timestamps sent
// on the wire; Clock controls deadlines and intervals.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) Timer
	// NewTicker returns a ticker that fires every d.
	NewTicker(d time.Duration) Ticker
}

// Timer is the subset of *time.Timer this package uses. C is a method
// because an interface cannot have fields.
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

// idleBarrier is implemented by a Clock that must know when every
// participant has finished reacting before it advances time.
//
// An event loop reports each time it is about to block, and the clock waits
// for those reports after delivering a timer. The loop's select receives
// without acting and marks itself busy in one place, so no case can be
// missed. The retransmission wheel reports for itself as well, since most of
// its ticks reach no connection.
//
// RealClock does not implement it.
type idleBarrier interface {
	// Register announces a participant whose parking matters. Called once,
	// when the loop starts.
	Register()
	// MarkIdle reports that the caller is about to block, and MarkBusy that
	// it has woken. A clock may only move time while every registered
	// participant is idle.
	MarkIdle()
	MarkBusy()
	// NoteHandoff reports that an item is being queued for another
	// participant, and TakeHandoff that it has been received. The sender
	// calls the first before sending and the receiver the second once it
	// holds the item. A channel send returns before the receiver runs, so
	// without this every participant could be parked while work is still
	// in flight.
	NoteHandoff()
	TakeHandoff()
}

// realClock uses the time package.
type realClock struct{}

// RealClock is the clock a connection uses unless another is configured.
var RealClock Clock = realClock{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

// AfterFunc runs f in its own goroutine after d. The returned timer's
// channel is nil. See funcTimerClock.
func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
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
