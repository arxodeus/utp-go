# libutp_conformance_test.go

Every comment in `libutp_conformance_test.go` at e56c9aa, verbatim.

## Line 11

```
// Behaviours matched to libutp, with the reference line numbers in the
// assertions so a future change can be checked against the same source.
```

## Line 14

```
// --- Zero-length ST_DATA ----------------------------------------------------
//
// libutp guards only the delivery to the application with `count > 0` and
// advances ack_nr unconditionally (utp_internal.cpp:2342-2355). It therefore
// acks a zero-length ST_DATA like any other packet.
//
// This implementation used to reject such a packet in the decoder, so it was
// dropped before the connection saw it, never acked, and the peer would
// retransmit it forever.
```

## Line 53 — func TestZeroLengthDataAdvancesTheAckNumber

```
// The next in-order packet, carrying nothing.
```

## Line 61 — func TestZeroLengthDataAdvancesTheAckNumber

```
// And the sequence space must keep moving afterwards, so a following
	// packet with real data is delivered rather than treated as a gap.
```

## Line 72

```
// --- SYN retransmission backoff ---------------------------------------------
//
// libutp doubles retransmit_timeout on every retransmission timeout
// (utp_internal.cpp:1179, applied at :1203), starting from 3000ms
// (utp_internal.cpp:2762). This implementation previously computed
// InitialTimeout * 1.5^attempts, which grows from the initial value rather
// than the current one, with a different factor.
```

## Line 88 — func TestSynRetransmissionBackoffDoubles

```
// Scaled down so the test is quick; the shape is what matters.
```

## Line 92 — func TestSynRetransmissionBackoffDoubles

```
// Connect to a peer that never answers, so every SYN times out.
```

## Line 100 — func TestSynRetransmissionBackoffDoubles

```
// Four attempts at 200/400/800ms is 1.4s of backoff; allow generously.
```

## Line 108 — func TestSynRetransmissionBackoffDoubles

```
// Gaps between successive SYNs must roughly double.
```

## Line 117 — func TestSynRetransmissionBackoffDoubles

```
// Doubling is 2.0; 1.5^n growth would give about 1.5. The timer wheel
		// has 25ms resolution and the scheduler adds noise, so the band is
		// wide enough to separate the two hypotheses without flaking.
```

## Line 140 — func TestConnectGivesUpAfterMaxAttempts

```
// libutp gives up on a connection attempt once retransmit_count reaches 2
	// in CS_SYN_SENT, which is three transmissions of the SYN in total
	// (utp_internal.cpp:1191).
```

## Line 176

```
// --- RTT estimator and RTO ---------------------------------------------------
//
// libutp, at utp_internal.cpp:1362-1380:
//
//	if (pkt->transmissions == 1) {
//	    ertt = (now_micros - pkt->time_sent) / 1000;   // milliseconds
//	    if (rtt == 0) { rtt = ertt; rtt_var = ertt / 2; }
//	    else {
//	        delta   = rtt - ertt;
//	        rtt_var = rtt_var + (abs(delta) - rtt_var) / 4;
//	        rtt     = rtt - rtt/8 + ertt/8;
//	    }
//	    rto = max(rtt + rtt_var * 4, 1000);
//	}
//
// This implementation keeps the same quantities in microseconds.
```

## Line 210 — func TestFirstRTTSampleIsAdoptedDirectly

```
// rto = max(400ms + 4*200ms, 1000ms) = 1200ms
```

## Line 218 — func TestRTOFloorIsOneSecond

```
// A fast path: rtt + rtt_var*4 = 20ms + 40ms = 60ms, well under the floor.
```

## Line 236 — func TestSubsequentRTTSamplesFollowLibutp

```
// rtt = 400ms, rtt_var = 200ms.
```

## Line 241 — func TestSubsequentRTTSamplesFollowLibutp

```
// delta   = 400 - 480 = -80ms
	// rtt_var = 200 + (80 - 200)/4 = 200 - 30 = 170ms
	// rtt     = 400 - 50 + 60 = 410ms
```

## Line 248 — func TestSubsequentRTTSamplesFollowLibutp

```
// rto = max(410 + 4*170, 1000) = max(1090, 1000) = 1090ms
```

## Line 252 — func TestRTTSamplesAreWholeMilliseconds

```
// libutp's estimator works in whole milliseconds: the sample is truncated
// (`ertt = (now - time_sent) / 1000`, utp_internal.cpp:1364) and rtt and
// rtt_var are integer millisecond counts. Fractions of a millisecond are
// dropped, not carried, and the RTO is libutp's to the millisecond.
```

## Line 264 — func TestRTTSamplesAreWholeMilliseconds

```
// As TestSubsequentRTTSamplesFollowLibutp: ertt 400 then 480 give
	// rtt 410, rtt_var 170, rto 1090. Carried in microseconds they give
	// 410.875, 170.288 and 1092.
```

## Line 272 — func TestRTTSamplesAreWholeMilliseconds

```
// On a path faster than a millisecond libutp's estimate stays 0, and
	// every sample is taken as the first (:1365). The loss probe, which is
	// not libutp's, needs the round trip anyway, and has its own.
```

## Line 282 — func TestRetransmittedPacketDoesNotUpdateRTT

```
// A retransmitted packet's ack cannot be attributed to a particular
// transmission, so it must not move the estimate. libutp guards on
// `pkt->transmissions == 1` (utp_internal.cpp:1362); this is Karn's algorithm.
```

## Line 293 — func TestRetransmittedPacketDoesNotUpdateRTT

```
// A packet sent twice, then acked with a wildly different measurement.
```
