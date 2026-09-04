package utp_go

import (
	"context"
	"sync/atomic"
	"time"
)

const (
	// defaultRetransmitTickInterval is the resolution of the retransmission
	// timer wheel.
	//
	// This bounds how late a retransmission can be, and -- more importantly --
	// it used to bound how *early* one could be. The wheel was previously
	// built per connection with an interval of InitialTimeout/4, one second,
	// which cannot express a 500 ms RTO at all: the timer landed on the next
	// tick, uniformly 0-1000 ms away, so roughly 6% of packets on a lossless
	// path were declared lost before their ack could arrive.
	//
	// 25 ms is fine enough that quantisation is negligible against any
	// realistic RTO. It is affordable only because the wheel is shared across
	// the whole socket: one ticker, not one per connection.
	defaultRetransmitTickInterval = 25 * time.Millisecond

	// defaultRetransmitSlots gives one revolution of 1.6s at the default
	// interval. Longer delays are handled by the wheel's rounds counter, so
	// this is a distribution parameter, not a ceiling.
	defaultRetransmitSlots = 64
)

// retransmitKey identifies one outstanding packet inside a socket-wide timer
// wheel. Connections share the wheel, so sequence numbers alone would collide.
type retransmitKey struct {
	scope uint64
	seq   uint16
}

// retransmitTimer is what the shared wheel stores: the packet to resend and
// where to deliver the expiry.
type retransmitTimer struct {
	packet  *packet
	deliver chan *packet
	ctx     context.Context
}

// retransmitTimers is a socket-wide retransmission timer wheel.
//
// One wheel per socket rather than one per connection. A 25 ms interval with
// a wheel per connection meant 40 timer wake-ups per second per connection --
// 40,000/s at a thousand connections, which cost about 25% of throughput.
// Shared, the tick rate is constant regardless of connection count and the
// per-tick work is proportional to the number of timers actually expiring.
type retransmitTimers struct {
	wheel     *timeWheel[*retransmitTimer]
	nextScope atomic.Uint64
}

func newRetransmitTimers(interval time.Duration, slots int) *retransmitTimers {
	r := &retransmitTimers{}
	r.wheel = newTimeWheel[*retransmitTimer](interval, slots, func(key any, t *retransmitTimer) {
		select {
		case t.deliver <- t.packet:
		case <-t.ctx.Done():
		default:
			// This connection's event loop is behind. Re-arm for the next
			// tick rather than blocking every other connection's timers
			// behind it, or dropping the timeout and stalling this
			// connection permanently.
			r.wheel.put(key, t, 0)
		}
	})
	return r
}

// newScope returns an identifier unique to one connection on this socket.
func (r *retransmitTimers) newScope() uint64 { return r.nextScope.Add(1) }

func (r *retransmitTimers) arm(key retransmitKey, t *retransmitTimer, delay time.Duration) {
	r.wheel.put(key, t, delay)
}

func (r *retransmitTimers) disarm(key retransmitKey) { r.wheel.remove(key) }

func (r *retransmitTimers) stop() { r.wheel.stop() }

// Len reports how many timers are armed across the whole socket.
func (r *retransmitTimers) Len() int { return r.wheel.Len() }
