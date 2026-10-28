# netem/queueing_delay_test.go

Every comment in `netem/queueing_delay_test.go` at e56c9aa, verbatim.

## Line 12 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// ConnectionMetrics.QueueingDelay must measure a queue, on the path this
// sender is sending into. An asymmetric path is what tells the difference.
//
// The two directions here are given very different propagation delays and no
// bottleneck at all, so neither direction has a standing queue: the right
// answer is approximately zero, whatever the delays are.
//
// It used to be the difference between the two directions. BaseDelay comes
// from the peer's measurement of our *outbound* path (libutp's `our_hist`,
// utp_internal.cpp:2016-2021) and PeerTsDiff is our own measurement of the
// *inbound* one (`their_delay`, :2000-2001); subtracting one from the other
// gives the path asymmetry, with any real queue buried inside it. On this
// link that produced about 90ms of queue on a path with none.
//
// The congestion controller was never affected -- it compares a sample
// against the base of the same series, which it has in hand. What this fed
// was the queueing delay Recorder reports.
```

## Line 30 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// One direction slow, the other fast, and nothing to queue behind.
```

## Line 73 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// Small and slow enough that the link is never near its capacity: the
	// point is a path with no queue, so any reported queue is an artefact.
```

## Line 115 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// What the old formula would have produced, computed from the same
	// samples, so the comparison is not a claim about a previous run.
```

## Line 130 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// The path asymmetry is 90ms. A queue reported anywhere near it is the
	// asymmetry, not a queue.
```

## Line 138 — func TestQueueingDelayMeasuresTheOutboundQueue

```
// And the control: the old formula really does report the asymmetry on
	// this link, so the assertion above is not passing for some other reason.
```

## Line 146 — func TestQueueingDelayTracksARealQueue

```
// The other half: the metric must still see a queue that is really there.
//
// The test above only proves it stops reporting a queue that is not. A metric
// hardwired to zero would pass that one, so this puts a real bottleneck under
// it and checks the reported queue against the emulated link's own
// measurement -- taken at the bottleneck, independently of anything the
// connection believes.
```

## Line 158 — func TestQueueingDelayTracksARealQueue

```
// A narrow bottleneck with a deep queue: LEDBAT will fill it to its
	// target and hold there, which is a standing queue by construction.
```

## Line 253 — func TestQueueingDelayTracksARealQueue

```
// Not equality: the link's figure is the mean over every packet it
	// carried, and the metric is sampled from the sender's timestamps at a
	// different cadence. Within a factor of three is enough to show it is
	// measuring the same thing rather than something unrelated.
```
