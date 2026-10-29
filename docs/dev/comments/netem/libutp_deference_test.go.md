# netem/libutp_deference_test.go

Every comment in `netem/libutp_deference_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 17 — func TestLibutpDeferenceToLossBasedFlow

```
// TestLibutpDeferenceToLossBasedFlow puts libutp through
// TestDeferenceToLossBasedFlow's experiment: the same shared bottleneck, the
// same loss-based competitor with the same head start, the same 4 MB, with
// libutp at both ends of the uTP flow. This library's classic LEDBAT runs the
// same experiment alongside it in each run, so the two are compared under the
// same conditions rather than against a figure taken another day.
//
// libutp's classic LEDBAT is what this library's default is meant to match,
// and "less than best effort" is the claim it is judged on. Until this ran,
// what libutp itself yields was assumed, not measured.
```

## Line 36 — func TestLibutpDeferenceToLossBasedFlow

```
// The two mixed pairings, to put a difference on the end that makes
		// it: the sender's controller, or the receiver's acknowledgements,
		// which here share the bottleneck with the competitor's data.
```

## Line 63 — func runMixedDeference

```
// runMixedDeference is the experiment with libutp at one end of the uTP flow
// and this library at the other.
```

## Line 119 — func runLibutpDeference

```
// runLibutpDeference is runDeference with libutp sending to libutp.
```

## Line 217 — func TestReceiverDoesNotCoalesceBehindAnotherFlow

```
// A receiver's acknowledgements that queue behind another flow's data are
// not its own queue, and coalescing them cost the sender's yielding: libutp's
// sender, sharing a bottleneck with a loss-based flow, left it 64.0% of its
// rate with this receiver coalescing to a third of libutp's acknowledgements,
// against 67.5% with libutp's receiver. This receiver now coalesces only
// once the way back climbs as its own acknowledgements make it climb
// (ackPathSelfQueued), and here acknowledges as libutp does. Measured: 0.95-
// 0.98 acknowledgements per data packet, libutp's 0.97; 0.35-0.41 before.
```

## Line 234 — func TestReceiverDoesNotCoalesceBehindAnotherFlow

```
// the competitor's packets are not uTP
```

## Line 242 — func TestReceiverDoesNotCoalesceBehindAnotherFlow

```
// ST_DATA
```

## Line 244 — func TestReceiverDoesNotCoalesceBehindAnotherFlow

```
// ST_STATE
```
