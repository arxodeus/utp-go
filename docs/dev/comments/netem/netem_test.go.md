# netem/netem_test.go

Every comment in `netem/netem_test.go` at e56c9aa, verbatim.

## Line 13

```
// The M1 gate. A harness you have not validated is worse than no harness: it
// produces numbers that look like measurements. These tests prove the three
// properties congestion results depend on -- configured bandwidth, configured
// delay and configured loss are what actually happens.
```

## Line 28 — type drainer

```
// drainer reads from an endpoint in the background, recording what arrives.
//
// Endpoint.ReadFrom blocks until a packet arrives or the endpoint closes, so
// stopping means closing the endpoint. Snapshot any link stats you care about
// *before* calling stop: once the endpoint is closed, packets still in flight
// are counted as receiver-overflow drops.
```

## Line 81 — func offerAtRate

```
// offerAtRate offers packets at `overload` times the given bit rate for the
// given duration, pacing in coarse bursts. Fine-grained tickers cannot pace
// reliably at sub-millisecond intervals, and a sender that accidentally
// offers *below* the bottleneck would make the bandwidth gate vacuous.
```

## Line 108

```
// --- Gate 1: a sender offering more than the link can carry achieves exactly
// the configured bandwidth, and no more. ------------------------------------
```

## Line 125 — func TestGateBandwidthIsHonoured

```
// Warm up first: offering at 2x fills the bottleneck queue, and
			// we want to measure the steady state, not the fill transient.
```

## Line 129 — func TestGateBandwidthIsHonoured

```
// With the queue full and staying full, bytes delivered over the
			// window is exactly the link rate -- the backlog neither grows
			// nor drains, so there is no residue to correct for. Measuring
			// across the fill or the drain would overstate the rate.
```

## Line 145 — func TestGateBandwidthIsHonoured

```
// Serialization is exact, so the only slack needed is for the
			// edges of the measurement window.
```

## Line 158 — func TestGateBandwidthDoesNotThrottleBelowRate

```
// A sender offering *less* than the bottleneck must not be throttled: the
// link should be transparent below its rate.
```

## Line 174 — func TestGateBandwidthDoesNotThrottleBelowRate

```
// 4 Mbps, well under the 10 Mbps link
```

## Line 188

```
// --- Gate 2: a configured one-way delay is the delay actually observed. -----
```

## Line 197 — func TestGateDelayIsHonoured

```
// No bandwidth limit: isolate propagation from serialization.
```

## Line 221 — func TestGateDelayIsHonoured

```
// The emulator can only add delay, never remove it, so every
			// sample must be at least the configured value.
```

## Line 226 — func TestGateDelayIsHonoured

```
// Scheduler wake-up latency is the only source of the excess.
			// The median, not the mean: this checks the delay the emulator
			// applies, and a mean let one sample the operating system held
			// up fail it -- 69 ms of 20 lifted the mean to 25.2 ms for a
			// 20 ms link, with every package's tests running at once. An
			// emulator that adds a few milliseconds too many moves the
			// median as surely as the mean.
```

## Line 240 — func TestGateRoundTripIsTwiceOneWay

```
// Round-trip delay across a symmetric link must be twice the one-way delay:
// this is the RTT a congestion controller will measure.
```

## Line 250 — func TestGateRoundTripIsTwiceOneWay

```
// Echo server on b.
```

## Line 288

```
// --- Gate 3: a configured loss rate is the loss rate actually observed. -----
```

## Line 297 — func TestGateLossRateIsHonoured

```
// No bandwidth limit, so the only drop cause is medium loss.
```

## Line 307 — func TestGateLossRateIsHonoured

```
// Pace lightly: an unpaced blast overflows the reader's inbox,
				// which is a real drop but not the one under test here.
```

## Line 319 — func TestGateLossRateIsHonoured

```
// 3 sigma on a binomial with n=40000.
```
