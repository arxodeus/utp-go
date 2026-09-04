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

func NowMicro() uint32 {
	return uint32(time.Now().UnixMicro())
}

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

func RandomUint16() uint16 {
	return uint16(fastrand.Uint32n(65535))
}
