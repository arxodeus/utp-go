# utp_utils.go

Every comment in `utp_utils.go` at e56c9aa, verbatim.

## Line 31 — func nowMicro

```
// nowMicro is the timestamp uTP puts on the wire: microseconds, truncated to
// 32 bits, from a clock that never jumps.
//
// libutp stamps packets from CLOCK_MONOTONIC (utp_utils.cpp:158-175), and
// guards even that against running backwards. The peer reads the difference
// between our stamp and its own clock as the one-way delay, so a wall clock
// stepped by NTP would become queue, or a delay below the base.
//
// The value is the wall clock as it stood when the process started, carried
// forward by the monotonic clock: the same number time.Now().UnixMicro() would
// give while nobody touches the clock, and unaffected when somebody does. Only
// differences between stamps carry meaning, so where it starts does not matter.
// History: HISTORY.md, "nowMicro".
```

## Line 48 — var wireClockEpoch, wireClockEpochMicros

```
// wireClockEpoch carries Go's monotonic reading, which time.Since uses.
```

## Line 54 — func wrappingSubUint32

```
// wrappingSubUint32 returns later-earlier over the uint32 ring.
//
// uTP timestamps are uint32 microseconds and wrap roughly every 71.6 minutes,
// so the difference of two of them must be computed in uint32 arithmetic.
// Go's unsigned subtraction already wraps, which is exactly the behaviour
// libutp relies on (utp_internal.cpp computes reply_micro as a uint32
// subtraction). This exists as a named, separately tested function rather
// than being written inline at each use.
```

## Line 66 — func timestampDiffMicros

```
// timestampDiffMicros returns how long ago a peer's uint32 microsecond
// timestamp was, as seen against our own clock. The result is the value uTP
// carries in the timestamp_difference_microseconds header field.
```

## Line 73 — func durationBetween

```
// durationBetween returns the time between two uint32 microsecond timestamps.
//
// The previous implementation was off by one on the wrapping branch
// (MaxUint32-earlier+later rather than 2^32-earlier+later) and returned a
// Duration built from a microsecond count without scaling it, so the value
// was a nanosecond-typed Duration holding microseconds.
```

## Line 83 — var randomUint16Source

```
// randomUint16Source, when set, is where random sequence numbers and
// connection ids come from instead of fastrand. The conformance corpus pins it
// to compare emitted packets against libutp byte for byte; nothing outside
// tests sets it.
//
// Atomic because a socket's reader can still be finishing a datagram after
// Close has returned -- answering it with a reset draws a random number -- and
// a test that pins the source next would otherwise race it.
```

## Line 97 — func randomUint16

```
// All 65536 values, as libutp's get_random truncated to 16 bits gives.
	// This was Uint32n(65535), which is [0, 65535) and never drew 65535.
```
