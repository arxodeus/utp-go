# netem/libutp_comparison_test.go

Every comment in `netem/libutp_comparison_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 15 — func TestLibutpOverEmulatedNetwork

```
// TestLibutpOverEmulatedNetwork runs real libutp over the emulated network and
// compares it against this library on identical links.
//
// This is the gap COMPATIBILITY.md named as its largest: every congestion
// control row there was *cited* — code read against utp_internal.cpp and
// matched by hand — and every number in BENCHMARKS.md compared this library
// against a previous version of itself, because libutp had never been run over
// the emulated network. Four flows go over each link, every sender against
// every receiver:
//
//	go->go          both ends this library, the BENCHMARKS.md configuration
//	libutp->go      libutp's congestion controller, our receiver
//	go->libutp      our congestion controller, libutp's receiver
//	libutp->libutp  the reference against itself
//
// Comparing the first two is the point: same link, same payload, same clock,
// different sender. The other two are the controls: each sender against the
// other receiver, so that a difference can be put on the end that makes it.
// Every flow is timed from the moment before the SYN to the receiver holding
// the last byte.
//
// What this is not: a claim that either implementation is better. The links
// are emulated, the machine is one machine, and libutp is being driven by the
// loop in libutp_flow_test.go rather than by a production embedder.
```

## Line 92 — func runComparisonFlow

```
// runComparisonFlow runs one transfer in one direction and returns its goodput
// in Mbps. The payload is verified every time: a fast transfer of the wrong
// bytes is not a result.
```

## Line 145 — func TestLibutpTransfersOverEmulatedNetwork

```
// TestLibutpTransfersOverEmulatedNetwork is the cheap gate: libutp completes a
// verified transfer over the emulated network in both directions. It runs on
// every `go test ./netem`, where the comparison table above is the expensive
// part.
//
// Its value is not the throughput number. It is that the harness driving
// libutp — clock, packet delivery, read draining, MTU — is wired up correctly,
// which is not something to take on trust: three defects in it were found by
// measuring, and all three made libutp look worse than it is. See
// KNOWN-LIMITATIONS.md.
```
