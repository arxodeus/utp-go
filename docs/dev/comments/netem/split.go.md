# netem/split.go

Every comment in `netem/split.go` at e56c9aa, verbatim.

## Line 10 — type DeliverySample

```
// DeliverySample is a flow's cumulative delivered byte count at a moment.
```

## Line 16 — const deliverySampleInterval

```
// deliverySampleInterval bounds how often a deliveryTimeline records.
```

## Line 19 — type deliveryTimeline

```
// deliveryTimeline records a DeliverySample at most every
// deliverySampleInterval, and always the latest count.
```

## Line 30 — func (*deliveryTimeline).record

```
// Keep the latest count, so the series ends at the true total.
```

## Line 43 — func ReceivedSeries

```
// ReceivedSeries converts a receiver's recorded metrics into a delivery
// series, from the bytes it accepted from its peer.
```

## Line 53 — func SplitWhileBothRan

```
// SplitWhileBothRan is a's share of the bytes the two flows delivered while
// both were running: from the later flow's first sample to the earlier
// flow's last. ok is false if they never overlapped or delivered nothing in
// the overlap.
//
// This is the split itself. A share or a Jain index taken from each flow's
// whole-transfer goodput is not: a flow squeezed while both run finishes
// later, with the link to itself for the rest, and its goodput catches up.
```

## Line 84 — func bytesAt

```
// bytesAt is the count as of t: the last sample at or before it.
```
