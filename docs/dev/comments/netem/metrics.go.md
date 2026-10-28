# netem/metrics.go

Every comment in `netem/metrics.go` at e56c9aa, verbatim.

## Line 9 — type Stats

```
// Stats is what one Link observed. All counters are cumulative.
```

## Line 11 — type Stats

```
// Offered is what the sender handed to the link.
```

## Line 15 — type Stats

```
// Delivered is what reached the destination's inbox.
```

## Line 19 — type Stats

```
// Dropped is the total, broken down by cause below.
```

## Line 24 — type Stats

```
// DroppedByMTU counts datagrams larger than the link's MTU. This is a
	// statement about size, not about congestion, and is kept apart from the
	// other two for that reason.
```

## Line 29 — type Stats

```
// PacketsFragmented counts datagrams larger than the link's MTU that were
	// forwarded anyway, because Config.FragmentOversized is set and the
	// sender did not forbid fragmentation. They are delivered, so they are
	// not dropped and not counted as such -- the number exists because "the
	// probe arrived" and "the probe arrived in one piece" are different
	// facts, and a path-MTU search can only tell them apart if something
	// records the difference.
```

## Line 39 — type Stats

```
// PacketsDuplicated counts the extra copies Config.DuplicateRate made.
```

## Line 47 — func (Stats).LossRate

```
// LossRate is the fraction of offered packets that did not arrive.
```

## Line 55 — func (Stats).MediumLossRate

```
// MediumLossRate is the fraction dropped by Config.LossRate alone, excluding
// congestion and receiver overflow. This is the number to compare against the
// configured loss rate.
```

## Line 65 — func (Stats).MeanQueueDelay

```
// MeanQueueDelay is the average time packets waited for the bottleneck.
```

## Line 73 — func (Stats).String

```
// String renders the stats for a test log.
```

## Line 83 — type QueueSample

```
// QueueSample is one observation of the bottleneck's state.
```

## Line 90 — func (*Link).Stats

```
// Stats returns a snapshot of this link's counters.
```

## Line 97 — func (*Link).QueueSamples

```
// QueueSamples returns the queueing-delay time series, oldest first.
// This is the standing-queue measurement a delay-based controller is judged
// on: LEDBAT is supposed to keep this bounded near its target.
```

## Line 108 — func (*Link).QueueDelayPercentile

```
// QueueDelayPercentile returns the p'th percentile (0..1) of observed
// queueing delay. The median and p95 say far more about a controller's
// behaviour than the mean, which a long idle tail flatters.
```

## Line 132 — func (*Link).ResetStats

```
// ResetStats clears counters and samples, for measuring a steady-state window
// after warm-up rather than including the handshake.
```

## Line 141 — type EndpointStats

```
// EndpointStats is what an Endpoint observed on its receive side.
```

## Line 145 — type EndpointStats

```
// PacketsDroppedFullInbox is packets that arrived while the application
	// was not reading fast enough.
```

## Line 150 — func (*Endpoint).Stats

```
// Stats returns a snapshot of this endpoint's receive counters.
```

## Line 161 — type Throughput

```
// Throughput is a goodput measurement over a wall-clock interval.
```

## Line 167 — func (Throughput).Bps

```
// Bps returns the rate in bits per second.
```

## Line 175 — func (Throughput).Mbps

```
// Mbps returns the rate in megabits per second.
```

## Line 178 — func (Throughput).String

```
// String renders the throughput for a test log.
```

## Line 183 — func FairnessIndex

```
// FairnessIndex returns Jain's fairness index for a set of flow rates: 1.0 is
// a perfectly even split, 1/n is one flow taking everything. It is the
// standard way to report how two flows shared a bottleneck.
```
