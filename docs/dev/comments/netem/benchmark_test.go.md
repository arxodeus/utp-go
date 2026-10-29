# netem/benchmark_test.go

Every comment in `netem/benchmark_test.go` at e56c9aa, verbatim.

## Line 18

```
// The M5 benchmark suite: one fixed set of link profiles, run the same way
// every time, so a congestion-control change can be judged rather than
// guessed at.
//
// Every profile is deterministic -- a fixed seed per link, and the emulator's
// loss, reordering and jitter all draw from it -- so two runs of the same
// binary produce the same numbers, and a difference between two binaries is
// the change and not the weather. What is *not* deterministic is our side's
// timing: it runs on the real clock with real goroutines, so single runs vary
// by a few percent. Numbers below are one run each unless stated; treat
// differences under about 5% as noise.
//
// Each profile is run benchmarkRepeats times and the median reported, because
// our side's run-to-run variance is real: a single retransmission timeout
// costs a full RTO and can halve a short transfer's goodput. The spread is
// reported alongside the median so a reader can see how much to trust it.
//
// Run with:
//
//	go test ./netem -run TestBenchmarkSuite -v
//
// It is skipped under -short.
```

## Line 41 — const benchmarkRepeats

```
// benchmarkRepeats is how many times each profile runs by default: once.
//
// One run is a gate, not a measurement. Everything this suite asserts is
// per-run -- the transfer completed, and the bytes that arrived are the bytes
// that were sent -- so a single pass catches a congestion controller that has
// stopped working. What it cannot do is produce a number worth quoting.
//
// It used to be three, and together with the libutp comparison that put
// `go test ./netem` at 634 seconds against Go's 600-second default per-package
// timeout: the harness gate failed unless you knew to pass -timeout, which is
// a poor way to greet anyone running the suite for the first time. Two
// measurement suites were most of that, and neither needs to run repeatedly to
// act as a gate.
//
// Set UTP_BENCHMARK_REPEATS to take a measurement. The tables in
// BENCHMARKS.md and KNOWN-LIMITATIONS.md are medians of seven, which is what
// the loss profiles need: their results are bimodal, one retransmission
// timeout dominating a short transfer, and three runs cannot separate the two
// modes.
```

## Line 62 — func benchmarkRepeatCount

```
// benchmarkRepeatCount is the repeat count, overridable for measurement runs.
```

## Line 72 — func benchmarkAlgorithms

```
// benchmarkAlgorithms is the set of congestion controllers the suite runs.
// Every profile runs under each, so the tables can be compared directly.
```

## Line 109 — func benchmarkProfiles

```
// Long enough that a single retransmission timeout is not a
			// large fraction of the run.
			//
			// At 512 KB these took about a second, and one RTO -- a second on
			// its own at the default floor -- made the result bimodal: nine
			// runs still landed in two clusters a factor of three apart, and
			// the median reported whichever cluster held five of them. A
			// benchmark row that swings on which mode the majority fell into
			// is not measuring the congestion controller.
```

## Line 142 — func benchmarkProfiles

```
// Long enough for several LEDBAT++ slowdown cycles. Everything
			// above finishes in one or two, where a slowdown is a large
			// fraction of the whole transfer and the measurement says more
			// about the transfer's length than about the controller.
```

## Line 211 — func TestBenchmarkSuite

```
// Two flows over one bottleneck: the fairness number LEDBAT is
			// judged on. See twoFlowProfiles for why there are two.
```

## Line 251 — func runProfileOnce

```
// runProfileOnce runs one transfer over one profile's link and returns what
// it achieved.
```

## Line 307 — type twoFlowProfile

```
// twoFlowProfile is a bottleneck shared by two identical flows that start
// together.
```

## Line 315 — func twoFlowProfiles

```
// twoFlowProfiles are the two-flow rows.
//
// The first is the original row and is kept as it was, so it stays
// comparable with every figure recorded against it. Its queue, 64KB at
// 8Mb/s, is 65ms, below LEDBAT's 100ms target, so whether it overflows
// depends on the controller: classic LEDBAT's two flows keep the queue short
// enough never to drop a packet, LEDBAT++'s drop 8-10 a run.
//
// The second takes the queue out of the question. It holds 2MB, about two
// seconds at 8Mb/s, which no controller here fills, so nothing is dropped
// under either and the flows can only yield to each other on delay. The
// payload is larger so the shared period outlasts slow start.
```

## Line 334 — type twoFlowResult

```
// twoFlowResult is one run of a two-flow profile.
```

## Line 337 — type twoFlowResult

```
// fairness is Jain's index over the two flows' goodputs. Each flow's
	// goodput covers its whole transfer, including any time it had the link
	// to itself after the other finished, which evens the two out: a 60/40
	// split while both ran still scores about 0.99.
```

## Line 342 — type twoFlowResult

```
// shared is the smaller flow's share of the bytes delivered while both
	// were running, which is the split itself.
```

## Line 345 — type twoFlowResult

```
// queueDrops counts packets the bottleneck discarded because its queue
	// was full.
```

## Line 405 — func sharedPeriodSplit

```
// sharedPeriodSplit is the smaller of two flows' shares of the bytes their
// receivers took in while both were running. It is 0 if the two never
// overlap. See SplitWhileBothRan.
```

## Line 416 — func benchmarkSeed

```
// benchmarkSeed is the profile's seed, or UTP_BENCHMARK_SEED when set.
//
// A profile's seed fixes which packets the emulator drops, so every repeat of
// a profile sees the same loss pattern -- as long as the sender offers the
// same packets in the same order. A change to what the sender puts on the
// wire moves every later loss onto different packets, and a comparison of
// two implementations on one seed is then partly a comparison of two loss
// patterns. Sweeping the seed is how to separate them.
```

## Line 433 — func benchmarkRACK

```
// benchmarkRACK is whether UTP_BENCHMARK_RACK asks for ConnectionConfig.RACK,
// so that the suite, deference and latecomer measurements can compare the
// two loss detectors in one binary, interleaved.
```
