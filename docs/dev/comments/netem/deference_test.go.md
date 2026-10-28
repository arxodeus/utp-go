# netem/deference_test.go

Every comment in `netem/deference_test.go` at e56c9aa, verbatim.

## Line 11 — func TestDeferenceToLossBasedFlow

```
// The experiment uTP is actually judged on: does it get out of the way of a
// loss-based flow sharing the same bottleneck?
//
// Everything else in this harness measures uTP against uTP, which is the easy
// case. "Less than best effort" is a claim about what happens when something
// greedy shows up, and until now nothing here could produce something greedy.
// netem.RunRenoFlow is that competitor -- Reno's congestion control exactly,
// with none of TCP's protocol; see reno.go for precisely what it does and does
// not model.
//
// Both flows run over one shared bottleneck (Network.ConnectShared), so they
// contend for one queue at one rate. Without that they would each get their
// own link and neither could crowd the other out, which is what made this
// measurement impossible before.
```

## Line 30 — func TestDeferenceToLossBasedFlow

```
// The competitor alone, to establish what the link gives one greedy flow
	// with nothing to share it with.
```

## Line 46 — func TestDeferenceToLossBasedFlow

```
// The number that matters: how much of the competitor's
			// bandwidth uTP took. A protocol that yields leaves the
			// competitor close to what it had alone.
```

## Line 54 — func TestDeferenceToLossBasedFlow

```
// The share above is from each flow's whole-transfer goodput,
			// which counts the competitor's head start and whichever flow
			// finishes alone. This one is the split while both were sending.
```

## Line 75 — func deferenceLinkConfig

```
// deferenceLinkConfig is the shared bottleneck both experiments run over.
//
// The queue is 256 KB, about 200ms at this rate. It has to be deeper than
// either controller's delay target for delay-based control to operate at all:
// on a queue shallower than the target, the queue overflows before the target
// is reached and a delay-based controller degenerates into a loss-based one,
// which would make this experiment measure nothing.
```

## Line 92 — func runRenoSolo

```
// runRenoSolo measures the competitor with the link to itself.
```

## Line 114 — func runDeference

```
// runDeference runs a uTP flow and the competitor over one shared bottleneck
// and returns what each achieved, plus the queue they left at the bottleneck.
```

## Line 126 — func runDeference

```
// One bottleneck, four directed pairs through it.
```

## Line 152 — func runDeference

```
// The competitor starts first and is given time to fill the queue, so the
	// uTP flow arrives at a bottleneck that is already busy -- which is the
	// situation it is supposed to detect and defer to.
```

## Line 200 — func runDeference

```
// The queue at the bottleneck itself, which is what a third party sharing
	// this link would experience.
```
