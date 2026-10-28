package utp

import (
	"sync/atomic"
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

// nowMicro returns the timestamp uTP puts on the wire: microseconds,
// truncated to 32 bits, from a monotonic clock. The peer reads the
// difference between its clock and our stamp as one-way delay, so a wall
// clock stepped by NTP would be misread as queueing delay.
//
// The value is the wall clock at process start advanced by the monotonic
// clock. Only differences between stamps matter.
func nowMicro() uint32 {
	return uint32(wireClockEpochMicros + time.Since(wireClockEpoch).Microseconds())
}

// wireClockEpoch carries Go's monotonic reading, which time.Since uses.
var (
	wireClockEpoch       = time.Now()
	wireClockEpochMicros = wireClockEpoch.UnixMicro()
)

// wrappingSubUint32 returns later-earlier modulo 2^32. uTP timestamps wrap
// about every 71.6 minutes, so differences must be computed in uint32
// arithmetic.
func wrappingSubUint32(later, earlier uint32) uint32 {
	return later - earlier
}

// timestampDiffMicros returns how long ago a peer's uint32 microsecond
// timestamp was, as seen against our own clock. The result is the value uTP
// carries in the timestamp_difference_microseconds header field.
func timestampDiffMicros(now, peerTimestamp uint32) time.Duration {
	return time.Duration(wrappingSubUint32(now, peerTimestamp)) * time.Microsecond
}

// durationBetween returns the time between two uint32 microsecond
// timestamps, allowing for wraparound.
func durationBetween(earlier uint32, later uint32) time.Duration {
	return time.Duration(wrappingSubUint32(later, earlier)) * time.Microsecond
}

// randomUint16Source, when set, replaces fastrand as the source of random
// sequence numbers and connection ids. Only tests set it.
//
// It is atomic because a socket's reader can still be answering a datagram
// with a reset, which draws a random number, after Close has returned.
var randomUint16Source atomic.Pointer[func() uint16]

func randomUint16() uint16 {
	if f := randomUint16Source.Load(); f != nil {
		return (*f)()
	}
	// Draw from all 65536 values.
	return uint16(fastrand.Uint32())
}

func maxUint32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
