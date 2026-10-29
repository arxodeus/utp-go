# netem/link_test.go

Every comment in `netem/link_test.go` at e56c9aa, verbatim.

## Line 11

```
// Properties beyond the three headline gates. A congestion test is only worth
// running if reordering, jitter and queue bounding behave as configured, and
// only worth keeping if it reproduces.
```

## Line 44 — func TestReorderingHappensAtConfiguredRate

```
// Count inversions: a delivered sequence number lower than one already
	// seen means a packet actually arrived late, not merely that it was
	// flagged.
```

## Line 135 — func TestJitterVariesDelayWithinBounds

```
// Assertions are on the distribution, not on the extreme. A single OS
	// scheduling hiccup moves the max by tens of milliseconds, and a harness
	// test that flakes gets deleted -- after which nothing is tested.
```

## Line 141 — func TestJitterVariesDelayWithinBounds

```
// The emulator can only add delay, never remove it, so nothing may arrive
	// earlier than base-jitter. This is an invariant, not a statistic.
```

## Line 146 — func TestJitterVariesDelayWithinBounds

```
// The mean of a uniform distribution sits at its centre.
```

## Line 153 — func TestJitterVariesDelayWithinBounds

```
// A loose ceiling to catch gross errors without flaking on scheduler noise.
```

## Line 159 — func TestQueueDelayBoundedByQueueSize

```
// The bottleneck queue is what bounds standing delay. A delay-based
// controller is judged on how far below this ceiling it keeps the queue, so
// the ceiling itself has to be right.
```

## Line 178 — func TestQueueDelayBoundedByQueueSize

```
// A full queue holds queueBytes, which drains at bw.
```

## Line 188 — func TestQueueDelayBoundedByQueueSize

```
// Persistent overload should drive the queue close to full.
```

## Line 197 — func TestSameSeedReproducesSameDrops

```
// Determinism is the whole reason for seeding. A congestion test that flakes
// gets deleted, and then nothing is tested.
```

## Line 250 — func TestTwoFlowsShareBottleneck

```
// Two flows over one link must contend for the same bottleneck. This is the
// mechanism the M5 fairness gates depend on; if flows did not actually share
// a queue, those gates would measure nothing.
```

## Line 261 — func TestTwoFlowsShareBottleneck

```
// Tag each flow in the payload so delivery can be attributed.
```

## Line 284 — func TestTwoFlowsShareBottleneck

```
// One feeder, alternating between the two flows.
	//
	// This is a property of the link, not of any congestion control: given an
	// evenly interleaved arrival pattern, a FIFO queue should deliver an even
	// split. Two independent goroutines cannot establish that, because what
	// each one manages to *offer* is itself decided by the Go scheduler, and
	// Jain computed over delivered counts then conflates the queue's fairness
	// with the scheduler's.
	//
	// That is not a hypothetical. With two goroutines the test failed at 0.885
	// inside a full-package run while passing alone; reducing each burst from
	// 50 packets to 4 -- the offered load had been ten times the link rate,
	// not the "~75%" its comment claimed -- made it far better but not
	// reliable, and it still produced 0.826 once in eight runs. One feeder
	// removes the variance rather than narrowing it.
```

## Line 312 — func TestTwoFlowsShareBottleneck

```
// Eight packets a tick, alternating, is about 160% of the link
			// between them: enough to keep the queue busy and contended
			// without the offered load itself becoming the variable.
```

## Line 326 — func TestTwoFlowsShareBottleneck

```
// warm up
```

## Line 349 — func TestTwoFlowsShareBottleneck

```
// The two flows together must not exceed the link.
```

## Line 356 — func TestTwoFlowsShareBottleneck

```
// Two identical greedy senders on one FIFO queue should land near an even
	// split. This is a property of the harness, not of any congestion control.
```
