# peer_fin_timeout_test.go

Every comment in `peer_fin_timeout_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 13

```
// When the peer's stream ends, libutp arms its retransmission timeout
// `min(rto * 3, 60)` milliseconds out (utp_internal.cpp:2358). The RTO is
// never below 1000 ms (:1380), so that is always 60 ms, and if anything is in
// flight check_timeouts then treats it as a timeout: every packet marked for
// resend, the window cut to one packet, slow start, and the oldest resent
// (:1144-1235). A peer that half-closes while libutp is still sending -- a
// request followed by a response -- costs libutp its window.
//
// This library does not copy it (DEVIATIONS.md, "Reaching the end of the
// peer's stream does not time out what is in flight"). Both halves are
// measured here: libutp's resend moves from its timeout to its next timeout
// pass after the FIN, and ours stays where it was.
```

## Line 26 — func libutpFirstResendAfterResponse

```
// libutpFirstResendAfterResponse is how long after sending its response
// libutp first resends it, with the peer's FIN arriving or not.
```

## Line 68 — func ourFirstResendAfterResponse

```
// ourFirstResendAfterResponse is the same, for this library on a virtual
// clock.
```

## Line 160 — func TestPeerFinDoesNotTimeOutWhatIsInFlight

```
// libutp's half, so a change in the vendored copy shows: the FIN pulls
	// its resend forward to its next timeout pass.
```

## Line 167 — func TestPeerFinDoesNotTimeOutWhatIsInFlight

```
// Ours: the FIN changes nothing.
```
