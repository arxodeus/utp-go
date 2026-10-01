package utp_go

import (
	"time"

	"github.com/valyala/fastrand"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func ClampUint32(value, min, max uint32) uint32 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// NowMicro is the timestamp uTP puts on the wire: microseconds, truncated to
// 32 bits, from a clock that never jumps.
//
// libutp stamps packets from CLOCK_MONOTONIC (utp_utils.cpp:158-175), and
// guards even that against running backwards. This used the wall clock,
// time.Now().UnixMicro(), which an NTP step moves by however far it is
// wrong. The peer reads the difference between our stamp and its own clock
// as the one-way delay, so a step back of a second became a second of queue,
// or a delay below the base that the base then followed down and kept.
//
// The value is the wall clock as it stood when the process started, carried
// forward by the monotonic clock: the same number time.Now().UnixMicro() would
// give while nobody touches the clock, and unaffected when somebody does. Only
// differences between stamps carry meaning, so where it starts does not matter.
func NowMicro() uint32 {
	return uint32(wireClockEpochMicros + time.Since(wireClockEpoch).Microseconds())
}

// wireClockEpoch carries Go's monotonic reading, which time.Since uses.
var (
	wireClockEpoch       = time.Now()
	wireClockEpochMicros = wireClockEpoch.UnixMicro()
)

// wrappingSubUint32 returns later-earlier over the uint32 ring.
//
// uTP timestamps are uint32 microseconds and wrap roughly every 71.6 minutes,
// so the difference of two of them must be computed in uint32 arithmetic.
// Go's unsigned subtraction already wraps, which is exactly the behaviour
// libutp relies on (utp_internal.cpp computes reply_micro as a uint32
// subtraction). This exists as a named, separately tested function rather
// than being written inline at each use.
func wrappingSubUint32(later, earlier uint32) uint32 {
	return later - earlier
}

// timestampDiffMicros returns how long ago a peer's uint32 microsecond
// timestamp was, as seen against our own clock. The result is the value uTP
// carries in the timestamp_difference_microseconds header field.
func timestampDiffMicros(now, peerTimestamp uint32) time.Duration {
	return time.Duration(wrappingSubUint32(now, peerTimestamp)) * time.Microsecond
}

// DurationBetween returns the time between two uint32 microsecond timestamps.
//
// The previous implementation was off by one on the wrapping branch
// (MaxUint32-earlier+later rather than 2^32-earlier+later) and returned a
// Duration built from a microsecond count without scaling it, so the value
// was a nanosecond-typed Duration holding microseconds.
func DurationBetween(earlier uint32, later uint32) time.Duration {
	return time.Duration(wrappingSubUint32(later, earlier)) * time.Microsecond
}

// randomUint16Source is where random sequence numbers and connection ids come
// from. It is a variable so the conformance corpus can pin it and compare
// emitted packets against libutp byte for byte; nothing outside tests
// reassigns it.
var randomUint16Source = func() uint16 { return uint16(fastrand.Uint32n(65535)) }

func RandomUint16() uint16 {
	return randomUint16Source()
}

func maxUint32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
