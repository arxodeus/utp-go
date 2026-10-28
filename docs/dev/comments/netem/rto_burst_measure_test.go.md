# netem/rto_burst_measure_test.go

Every comment in `netem/rto_burst_measure_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 17 — func TestRTOBurstMeasure

```
// What each sender puts on the wire at a retransmission timeout.
//
// A 16MB transfer over a 10ms, 20 Mb/s link, with the data direction blacked
// out for 3 seconds starting 1 second in. Every data packet offered to the
// link during the blackout is logged, grouped into bursts. The receiver is
// ours in both modes; only the sender differs.
//
// Observed (see KNOWN-LIMITATIONS.md, section 2f):
//
//	libutp:        one packet at the timeout (2.0-2.5s) -- the oldest -- and
//	               nothing more before the blackout ends.
//	ours, before:  the whole window, 75 packets, at ~2.0s, out of order;
//	               then 48-66 of them again at ~3.0s, without the backoff
//	               doubling. Three runs.
//	ours, after:   one packet, the oldest, at 2.04-2.06s, and nothing more.
//	               Two runs.
//
// libutp marks every packet in flight need_resend and resends only the
// oldest (utp_internal.cpp:1230-1252); the rest wait for flush_packets and
// the congestion window, or come back one per acknowledgement through the
// fast-timeout retry. The regression test for that is
// TestRetransmissionTimeoutResendsOnlyTheOldest, which is deterministic. This
// is the measurement behind it, and it takes about 25 seconds, so it runs
// only when UTP_RTO_BURST_MEASURE is set.
```

## Line 74 — func measureRTOBursts

```
// An ST_DATA: type 0 in the high nibble, version 1 (BEP 29).
```

## Line 93 — func measureRTOBursts

```
// Timed from the first data packet, not from here.
```

## Line 127 — func measureRTOBursts

```
// Bursts during the blackout: packets within 5ms of the previous one.
```
