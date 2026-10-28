package utp

import (
	"context"
	"sync/atomic"
	"time"
)

const (
	// defaultRetransmitTickInterval is the resolution of the retransmission
	// timer wheel: how late a retransmission can fire. A timer never fires
	// early. The wheel is shared by every connection on a socket, so this
	// costs one ticker per socket.
	defaultRetransmitTickInterval = 25 * time.Millisecond

	// defaultRetransmitSlots gives one revolution of 1.6 s at the default
	// interval. Longer delays wrap; this does not bound the delay.
	defaultRetransmitSlots = 64
)

// retransmitKey identifies one outstanding packet inside a socket-wide timer
// wheel. Connections share the wheel, so sequence numbers alone would collide.
type retransmitKey struct {
	scope uint64
	seq   uint16
}

// retransmitTimer is an entry in the wheel: the packet to resend and where
// to deliver the expiry.
type retransmitTimer struct {
	packet  *packet
	deliver chan *packet
	ctx     context.Context
	// wake starts the connection's event loop if it is resting.
	wake func()
}

// retransmitTimers is a retransmission timer wheel shared by every
// connection on a socket, so the tick rate does not grow with the number of
// connections.
type retransmitTimers struct {
	wheel     *timeWheel[retransmitKey, retransmitTimer]
	nextScope atomic.Uint64
}

func newRetransmitTimers(interval time.Duration, slots int) *retransmitTimers {
	return newRetransmitTimersWithClock(interval, slots, RealClock)
}

// newRetransmitTimersWithClock returns a retransmission wheel driven by clk.
func newRetransmitTimersWithClock(interval time.Duration, slots int, clk Clock) *retransmitTimers {
	r := &retransmitTimers{}
	barrier, _ := clk.(idleBarrier)
	r.wheel = newTimeWheelWithClock(interval, slots, clk, func(key retransmitKey, t retransmitTimer) {
		// The connection has not taken this yet. See idleBarrier.NoteHandoff.
		if barrier != nil {
			barrier.NoteHandoff()
		}
		select {
		case t.deliver <- t.packet:
			if t.wake != nil {
				t.wake()
			}
		case <-t.ctx.Done():
			if barrier != nil {
				barrier.TakeHandoff()
			}
		default:
			if barrier != nil {
				barrier.TakeHandoff()
			}
			// The connection's event loop is behind. Re-arm for the next tick
			// rather than blocking the other connections' timers or dropping
			// the timeout.
			r.wheel.put(key, t, 0)
		}
	})
	return r
}

// newScope returns an identifier unique to one connection on this socket.
func (r *retransmitTimers) newScope() uint64 { return r.nextScope.Add(1) }

func (r *retransmitTimers) arm(key retransmitKey, t retransmitTimer, delay time.Duration) {
	r.wheel.put(key, t, delay)
}

func (r *retransmitTimers) disarm(key retransmitKey) { r.wheel.remove(key) }

func (r *retransmitTimers) stop() { r.wheel.stop() }

// Len reports how many timers are armed across the whole socket.
func (r *retransmitTimers) Len() int { return r.wheel.Len() }
