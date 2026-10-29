# native/libutp/ack_turnaround_test.go

Every comment in `native/libutp/ack_turnaround_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 19 — func TestAckTurnaround

```
// TestAckTurnaround measures how long each implementation's receiver takes to
// acknowledge a data packet: from the moment the relay writes the packet to
// the receiver's socket to the moment the relay reads the first
// acknowledgement that covers it, cumulatively or in a selective ack.
//
// COMPATIBILITY.md said this could not be measured, because both
// implementations send deferred acknowledgements when something outside them
// says so -- libutp when its embedder calls utp_issue_deferred_acks, this
// library when an event-loop pass ends -- so a test would time the harness.
// Over real sockets the objection goes away: each runs with its production
// trigger. libutp's bridge drains its socket and then issues the deferred
// acks, which is what utp.h asks of an embedder; this library runs on utpnet.
// Both are timed on the relay's one clock, so the kernel and the relay add the
// same to both.
//
// The sender is held fixed while the receiver changes, so that a difference
// between two rows with the same sender belongs to the receiver. The gate is
// that, for each sender and path, our receiver's median turnaround stays
// within ackTurnaroundSlack of libutp's. Delaying our flush by 1 ms fails it
// on every path (medians 0.76-1.17 ms against 0.06-0.18 ms), and leaves
// libutp's rows unchanged.
//
// The relay paces each datagram to its time (relaySpin). Before it did, it
// delivered a 100 Mb/s path's packets in clumps of about ten, and libutp's
// loop, draining each clump at once, appeared to send 0.22-0.33
// acknowledgements per data packet; paced, it sends 0.58-0.62.
//
// The relay runs in a process of its own (relay_process_test.go). Spinning in
// this one, it kept the Go runtime awake, and a runtime with a goroutine
// running wakes the socket's reader sooner than an idle one: our receiver's
// median was 80 us with the relay here and is 90 us without it, against
// libutp's 50 either way, and our 90th percentile, 240 us here, is libutp's
// 1.1 ms without it. An application's runtime is not kept awake for it.
```

## Line 68 — func TestAckTurnaround

```
// Each pairing runs ackTurnaroundRounds times, the pairings
		// interleaved, and the gate compares each receiver's best median.
		//
		// It used to compare the 90th percentile, and failed three times
		// under the full suite -- netem's CPU-heavy tests run alongside --
		// with our p90 at 1.9, 2.8 and 3.7 ms against libutp's 0.55-1.16 ms,
		// on code that measures 0.28 against 0.12 alone. Load lands in the
		// tail, and it lands harder on a receiver that hands each packet
		// between goroutines than on one that handles it inline on a thread,
		// so the tail measured the machine. A defect that delays the flush
		// delays every acknowledgement, the median with them: a 1 ms delay
		// moves it by a millisecond, well clear of the slack.
```

## Line 87 — func TestAckTurnaround

```
// Data runs sideA -> sideB (toGo) when the sender faces
						// sideA, which it does in every pairing but go->libutp.
```

## Line 99 — func TestAckTurnaround

```
// Our receiver's rows only. libutp's own pair left one
						// packet unacknowledged inside the trace window twice
						// under the full suite's load; it is the reference,
						// and the harness's window closing on its last
						// acknowledgement is not something this test is about.
```

## Line 116 — func TestAckTurnaround

```
// timing under the race detector; see raceEnabled
```

## Line 121 — func TestAckTurnaround

```
// a subtest failed and said why
```

## Line 131 — const ackTurnaroundRounds

```
// ackTurnaroundRounds is how many times each pairing runs.
```

## Line 134 — func bestP50

```
// bestP50 is the lowest of the runs' medians.
```

## Line 145 — const ackTurnaroundSlack

```
// ackTurnaroundSlack is how much slower than libutp's our receiver's median
// may be. With the relay in its own process, and stamping deliveries before
// its write (relayPath.deliver), ours is 10-20 us slower (12 runs each: 70-80
// us against 50-60 at 10 Mb/s, 40 against 30 at 100 Mb/s), the Go runtime
// waking the socket's reader and a slower turn-round. When an
// acknowledgement waited for up to four packets at 100 Mb/s whatever the
// return path, ours was 220-240 us against 30, which this fails.
```

## Line 160 — func turnaround

```
// turnaround pairs each data packet delivered to the receiver with the first
// acknowledgement it sent afterwards that covers the packet.
```

## Line 206 — func covers

```
// covers reports whether an acknowledgement acknowledges seq, cumulatively or
// selectively. Selective-ack bit i, least significant first, is ack + 2 + i.
```

## Line 219 — func runPair

```
// runPair runs one verified transfer through a relay with cfg in both
// directions.
```

## Line 262 — func runPair

```
// The relay's libutp-facing side faces our sender here.
```
