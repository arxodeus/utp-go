# netem/latecomer_test.go

Every comment in `netem/latecomer_test.go` at e56c9aa, verbatim.

## Line 11 — func TestLatecomerShare

```
// The latecomer experiment: the failure classic LEDBAT is known for, and the
// one LEDBAT++ exists to fix.
//
// A delay-based controller decides how much queue it is causing by comparing
// the delay it measures now against the lowest delay it has ever seen. That
// only works if it ever saw the path empty. A flow that starts while another
// flow already has the bottleneck queue full measures that queue as if it
// were the path itself: its "base delay" is wrong by exactly the standing
// queue, it concludes it is causing no delay at all, and it stops yielding.
//
// The result is backwards. The flow that arrives *later* is the one that
// misbehaves, and it takes throughput from the flow that was already there.
// RFC 6817 acknowledges this; LEDBAT++ §4.4 answers it with periodic
// slowdowns, which drop the window to two packets for two round trips so the
// queue drains and the base delay can be measured against an empty path.
//
// This test does not assert that LEDBAT++ wins. It measures the split both
// ways and reports it, and fails only on the thing that would make the
// measurement meaningless -- a flow that got no bandwidth at all.
```

## Line 51 — func TestLatecomerShare

```
// Goodputs cover each flow's whole transfer, including the
			// incumbent's head start and the latecomer's finish alone. This
			// is the split while both were sending.
```

## Line 66 — func runLatecomer

```
// runLatecomer starts one flow, lets it fill the bottleneck queue, then starts
// a second, and returns each flow's goodput in Mbps.
//
// Both flows carry the same payload, so the comparison is of how long each
// took, not of how much each was asked to move.
```

## Line 78 — func runLatecomer

```
// A deep queue, so a standing queue can actually build and be measured.
	// 256 KB at 10 Mbps is about 200 ms of buffering -- ordinary for
	// consumer broadband, and deeper than either algorithm's delay target,
	// which is what makes delay-based control operative at all here.
```

## Line 122 — func runLatecomer

```
// Long enough for the incumbent to fill the queue, so the latecomer's
	// very first delay samples are taken against a full one. That is the
	// whole point: the latecomer's base delay is wrong from its first packet.
```

## Line 159 — func TestBaseDelayTracking

```
// The base-delay estimate is the thing the latecomer problem corrupts, so
// measure it directly rather than only through throughput: run a flow on a
// path with a standing queue and watch what it believes the empty path costs.
//
// A LEDBAT++ flow drops to two packets periodically, which drains the queue,
// so its base delay should track the real one. A classic LEDBAT flow has no
// such mechanism and can only ever revise its estimate downwards by luck.
```

## Line 208 — func TestBaseDelayTracking

```
// The standing queue this flow left behind. Lower is better: it
			// is the delay every other user of this path pays because of us.
			//
			// Measured from the round trip, not from the controller's own
			// base-delay estimate. The controller's estimate is exactly what
			// goes wrong here, so asking it how much queue it is causing is
			// asking the defendant for the verdict -- see Summary.
```
