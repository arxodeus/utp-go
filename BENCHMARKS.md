# Benchmarks

Every number here came from `go test ./netem -run TestBenchmarkSuite -v` and
`go test ./netem -run TestLatecomerShare -v`, on the emulated network
described in [HARNESS.md](HARNESS.md). Nothing in this file is estimated,
extrapolated, or carried over from a previous run.

## How to reproduce them

```sh
UTP_BENCHMARK_REPEATS=7 UTP_BENCHMARK_OUT_DIR=/tmp/bench go test -timeout 60m ./netem/
```

A plain `go test ./netem/` runs each profile once, which gates the suite -- the
assertions are all per-run -- but does not produce a number worth quoting. Every
table here is a median of seven with the range beside it.

## How to read them

Each link profile has a fixed seed, and the emulator's loss, reordering,
jitter and queueing all draw from it, so the *network* is identical between
runs. Our side is not: it runs on the real clock with real goroutines. Each
profile therefore runs seven times and the **median** is reported, with the
full range beside it. Where the range is wide, no amount of averaging hides it, and the range is
printed so it cannot be mistaken for precision.

**The two loss profiles were lengthened after these numbers exposed a defect in
the benchmark itself.** At 512 KB and 256 KB they took about a second, and a
single retransmission timeout -- a second on its own at the default floor --
made the result bimodal: nine runs landed in two clusters a factor of three
apart, and the median reported whichever cluster held five of them. A row that
swings on which mode the majority fell into is not measuring the congestion
controller, it is measuring the startup transient. They now carry 4 MB and
2 MB, and the 1% loss range tightened from 1.04-3.19 to 3.91-5.25.

That also means the loss rows in the historical tables below are not
comparable with the ones above: they were measured on the shorter profiles,
and were mostly reporting how one RTO happened to fall.

**A fixed seed is one loss pattern, and it is only the same pattern for the
same sender.** The seed decides which offered packets are dropped, in the order
they are offered. Change what the sender puts on the wire -- a different
retransmission, one packet more or less at some instant -- and every later
loss lands on different packets. Repeats on one seed are then samples of one
pattern, not of the sender. Comparing two versions of the library this way
compares two patterns as much as two implementations. It did exactly that
once: LEDBAT++ at 1% loss looked 32% slower after the retransmission-timeout
fix, with ranges that did not overlap, and across twelve seeds it was 2%
(KNOWN-LIMITATIONS.md, 2f). To compare versions, sweep the seed:

```sh
for seed in $(seq 1 12); do
  UTP_BENCHMARK_SEED=$seed UTP_BENCHMARK_REPEATS=1 go test ./netem -run 'TestBenchmarkSuite/.*/Broadband,_5%_loss$' -v
done
```

**The two tables below were regenerated in full** on 2026-10-10, at commit
`6f25766`, seven runs a profile with nothing else running. Since the previous
regeneration classic LEDBAT's window grows by a round trip's worth per round
trip, a cut for reordering is undone, and fewer allocations are made per
packet; LEDBAT++ grows at the draft's rate. No row is carried over.

One row moved for a reason not yet traced: two LEDBAT++ flows on the 64 KB
queue split 43/57 where they had split 49/51, since the MTU search began at
the ceiling (KNOWN-LIMITATIONS.md, bisected).

The **standing queue** column had silently lost its precision between the
two: since commit `9e90d17` the round trip the metrics report is libutp's, in
whole milliseconds, and the column (round trip p50 less its minimum)
inherited it -- 6 ms on Broadband at 1% loss for a queue of about 3. It now
reads the same estimator kept in microseconds (`ConnectionMetrics.FineRTT`).

Two columns describe queueing, and they are not the same thing:

- **`standing queue p50`** is the round trip above the lowest round trip this
  flow ever saw. It is the delay this flow imposed on everyone else sharing
  the path, and it is measured, not believed.
- **`qdelay p50`** is what the congestion controller *thought* it was causing:
  `PeerTsDiff - BaseDelay`, where `BaseDelay` is the controller's own estimate
  of the empty path.

They diverge, and the divergence is the point. On a 40 ms path carrying a
sustained transfer, classic LEDBAT reported a `qdelay` of 500µs while its
round trip sat at 80 ms. It was causing 38 ms of queue and could not see it,
because it was subtracting its own queue from itself. Any judgement about
"less than best effort" has to come from the standing-queue column.

To regenerate:

```
go test ./netem -run TestBenchmarkSuite -v
UTP_BENCHMARK_OUT_DIR=/tmp/bench go test ./netem -run TestBenchmarkSuite
go test ./netem -run 'TestLatecomerShare|TestBaseDelayTracking' -v
go test ./netem -run TestDeferenceToLossBasedFlow -v
```

## Classic LEDBAT (the default)

This is what a connection gets unless it asks for something else. It matches
libutp.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 | standing queue p50 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 91.65 Mbps | 86.24-92.07 | 366ms | 0.00% | 99410B | 154967B | 0.8% | 5.466ms | 9.126ms | 5.544ms |
| Broadband (20ms, 10Mbps, no loss) | 6.54 Mbps | 6.44-6.64 | 1.282s | 0.00% | 53200B | 78453B | 0.3% | 4.227ms | 17.695ms | 3.635ms |
| Broadband, 1% loss | 6.03 Mbps | 5.95-6.04 | 5.564s | 0.75% | 41573B | 77828B | 0.1% | 2.144ms | 15.758ms | 3.285ms |
| Broadband, 5% loss | 2.61 Mbps | 2.02-2.67 | 6.421s | 4.76% | 18019B | 32609B | 0.1% | 1.086ms | 3.809ms | 2.667ms |
| High BDP (100ms, 20Mbps, no loss) | 2.15 Mbps | 2.13-2.16 | 7.789s | 0.00% | 75168B | 113555B | 0.2% | 1.071ms | 2.89ms | 1.6ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 4.95 Mbps | 4.89-4.98 | 847ms | 0.00% | 38138B | 56939B | 0.6% | 1.805ms | 4.098ms | 3.087ms |
| Long transfer (20ms, 10Mbps, 8MB) | 9.11 Mbps | 8.86-9.13 | 7.366s | 0.10% | 88006B | 116474B | 0.0% | 33.141ms | 50.075ms | 33.901ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 4.42 Mbps | 4.39-4.45 | 948ms | 1.55% | 31406B | 47382B | 0.6% | 1.42ms | 4.574ms | 2.166ms |
| Two flows, 8Mbps bottleneck | 6.65 Mbps total | 6.31-6.79 | | | | | | | | Jain 1.000; split while both ran 50/50 (worst 48/52); 0 queue drops |
| Two flows, 8Mbps, 2MB queue | 7.71 Mbps total | 7.71-7.77 | | | | | | | | Jain 1.000; split while both ran 50/50 (worst 49/51); 0 queue drops |

## LEDBAT++

Opt in with `ConnectionConfig.CongestionAlgorithm = AlgorithmLEDBATPP`.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 | standing queue p50 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 75.95 Mbps | 73.12-76.68 | 442ms | 0.00% | 104177B | 261836B | 0.6% | 4.404ms | 16.187ms | 4.28ms |
| Broadband (20ms, 10Mbps, no loss) | 6.14 Mbps | 6.09-6.19 | 1.366s | 2.81% | 69693B | 144598B | 6.4% | 17.746ms | 50.231ms | 18.785ms |
| Broadband, 1% loss | 2.20 Mbps | 2.14-2.26 | 15.238s | 0.78% | 16512B | 65364B | 3.7% | 748µs | 3.18ms | 1.36ms |
| Broadband, 5% loss | 0.57 Mbps | 0.47-0.61 | 29.178s | 5.07% | 5485B | 11358B | 10.6% | 629µs | 2.212ms | 2.082ms |
| High BDP (100ms, 20Mbps, no loss) | 4.38 Mbps | 4.37-4.38 | 3.834s | 0.00% | 298232B | 573909B | 23.4% | 25.789ms | 88.923ms | 25.718ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 1.69 Mbps | 1.44-3.32 | 2.487s | 6.16% | 30609B | 89284B | 8.0% | 2.723ms | 11.518ms | 4.39ms |
| Long transfer (20ms, 10Mbps, 8MB) | 8.72 Mbps | 5.87-8.73 | 7.696s | 0.36% | 88481B | 144144B | 3.1% | 32.681ms | 47.19ms | 33.528ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 3.38 Mbps | 3.13-3.42 | 1.242s | 1.80% | 24757B | 41446B | 7.5% | 1.388ms | 9.609ms | 2.442ms |
| Two flows, 8Mbps bottleneck | 6.16 Mbps total | 4.33-6.28 | | | | | | | | Jain 0.995; split while both ran 43/57 (worst 33/67); 11 queue drops |
| Two flows, 8Mbps, 2MB queue | 7.69 Mbps total | 7.52-7.81 | | | | | | | | Jain 1.000; split while both ran 48/52 (worst 47/53); 0 queue drops |

## What LEDBAT++ costs, and what it buys

Read as a throughput table, LEDBAT++ is slower on seven of eight profiles, and
by far the most on the lossy ones: 2.20 against 6.03 Mbps at 1% loss, 0.57
against 2.61 at 5%. Part of that gap is classic LEDBAT's loss probe, which
LEDBAT++ does not use (below); most of it is that LEDBAT++ grows its window
far more slowly after a loss.

**It wins where the window has to get large.** High BDP (100 ms, 20 Mbps):
2.15 -> 4.38 Mbps, mean congestion window 75 KB -> 298 KB. Classic LEDBAT
grows at a flat 3000 bytes per round trip whatever the path, so on a long fat
link it never reaches the bandwidth-delay product; LEDBAT++'s gain scales with
the base RTT (§4.2) and its slow start is exponential, so it does.

**Alone on a link, it does not leave less queue.** This file used to say
LEDBAT++ left a seventh of classic LEDBAT's standing queue on the 8 MB
transfer (4.76 against 32.26 ms) and a thirty-fifth on a 256 KB queue (1.1
against 38.1 ms). Those were measured when LEDBAT++ read its queueing delay
from one-way timestamps. Since it measures round trips, as the draft's §4.5
says, it aims for the draft's 60 ms target and gets there. Now, three runs each
of `TestBaseDelayTracking`'s lone flow on a 256 KB queue:

| | Standing queue p50 |
| --- | --- |
| LEDBAT | 37.0-37.4 ms |
| LEDBAT++ | 44.5-46.9 ms |

and on the 8 MB transfer above, 33.5 ms against classic LEDBAT's 33.9 -- a
64 KB queue holds only 52 ms at 10 Mb/s, below either controller's target,
so both fill it.

**What it buys is deference.** Where the two differ is with loss-based
traffic on the link, which is the case uTP exists for: classic LEDBAT takes
about two thirds of it, LEDBAT++ about a third, below.

**It is slower on short paths on purpose.** §4.2 sets
`GAIN = 1 / min(16, ceil(2*TARGET/base))`, which on a 20 ms path is 1/6 -- six
times slower than Reno, where classic LEDBAT's 3000 bytes per round trip is
about twice *faster* than Reno. A background protocol that outpaces TCP is not
doing its job. The throughput this costs on an otherwise-empty link is the
price of not taking it from someone else on a busy one.

### Deference to a loss-based flow

`TestDeferenceToLossBasedFlow`. This is the experiment uTP is actually judged
on, and until now this harness could not run it.

The competitor is `netem.RunRenoFlow`: TCP Reno's congestion control exactly --
slow start, one segment per round trip in congestion avoidance, fast
retransmit on three duplicate acknowledgements, fast recovery, RFC 6298
timers with Karn's algorithm -- and none of TCP's protocol. It is not TCP and
`netem/reno.go` says so at length; what matters here is the property that
makes TCP the thing uTP must defer to, which is that it fills the bottleneck
queue and only backs off on loss.

Both flows share one bottleneck (`Network.ConnectShared`, added for this),
20 ms each way, 10 Mbps, a 256 KB queue. The competitor starts first and is
given 1.5 s to fill the queue, so the uTP flow arrives at a link that is
already busy. Baseline: the competitor alone gets **5.67 Mbps**.

Re-measured after the LEDBAT++ fixes, seven runs each, medians with ranges.
The competitor alone: **5.66 Mbps** (5.60-6.00). "uTP's share" is from each
flow's whole-transfer goodput, as before, and counts the competitor's head
start and whichever flow finishes alone; "while both ran" is the split of the
bytes delivered while both were sending, which is the split itself.

| | uTP | Competitor | Competitor kept | uTP's share | While both ran | Bottleneck queue p50 |
| --- | --- | --- | --- | --- | --- | --- |
| LEDBAT | 5.72 Mbps | 3.79 Mbps | 65% (62-70) | 60% | **69%** (65-75) | 23.7 ms (16-27) |
| LEDBAT++ | 3.71 Mbps | 4.82 Mbps | 84% (62-99) | 44% | **31%** (23-33; one run 55) | 12.8 ms (11-27) |

Classic LEDBAT does not yield. While both are sending it takes about 69% of a
shared link from a loss-based flow -- more than an equal share, against the
traffic it is supposed to defer to -- and costs that flow a third of its
throughput, while leaving about 24 ms of queue for anything else on the path.

LEDBAT++ defers: fourteen runs of fifteen took 23-33% while both ran, one
55%. That row is fifteen runs, and it is with LEDBAT++ measuring its queueing
delay from round trips, as the draft's §4.5 says. With the one-way delay it
used before, fifteen runs deferred in only five (27-30%) and took 50-73% in
the other ten, with a median queue of 21 ms; a seven-run measurement of that
version had looked better than it was, five runs in seven deferring.

The first measurement here, one run on an older build, gave 60% and 44% by
goodput share and 24.6 ms and 2.5 ms of queue. The goodput shares still agree;
the 2.5 ms does not reproduce.

This is the measurement that was missing, and it reverses the reading of the
throughput tables. Classic LEDBAT is faster in those tables *because it is not
doing the one thing the protocol exists to do*.

### The latecomer experiment

`TestLatecomerShare`. One uTP flow runs until it has filled the bottleneck
queue; a second joins 1.5 s later, so every delay sample it will ever take
begins against a full queue. Its idea of the empty path is wrong from its
first packet -- the failure RFC 6817 acknowledges and LEDBAT++ §4.4 answers.

Re-measured, seven runs each, medians; the latecomer's share is of the bytes
delivered while both were sending.

| | Incumbent | Latecomer | Ratio | Latecomer's share while both ran |
| --- | --- | --- | --- | --- |
| LEDBAT | 7.43 Mbps | 5.75 Mbps | 0.77 | **20.5%** (20.3-20.7) |
| LEDBAT++ | 4.16 Mbps | 6.57 Mbps | 1.58 | **71%** (70.2-71.9) |
| LEDBAT++, round-trip delay | | | | 69.7% (59.0-71.3, five runs) |
| LEDBAT++, before its fixes | 4.04 Mbps | 4.25 Mbps | 1.05 | 46.8% (45.7-47.3) |

Classic LEDBAT's incumbent keeps about 80% of the link while both run: here
the flow already holding the queue wins, not the latecomer. That is the first
few seconds. Over 16 MB a flow (`TestLatecomerShareOverTime`, opt-in) classic
LEDBAT's latecomer climbs steadily, 18% of the first two seconds to 59% of
the last -- the latecomer advantage, arriving slowly.

**LEDBAT++'s latecomer takes 71%, and that is a regression.** It was 46.8% on
the build before the fixes to LEDBAT++'s slowdowns, and the change that moved
it is letting the ramp out of a slowdown run to its target rather than stop on
delay -- which is what the draft says, and what fixed two flows that start
together. Measuring the delay from round trips, as the draft does, did not
change it. See KNOWN-LIMITATIONS.md, "LEDBAT++'s latecomer takes most of the
link"; it is open. The first measurement here, one run on an older build, was
a ratio of 1.03.

### Why the default is still classic LEDBAT

On the evidence above, LEDBAT++ is the better congestion controller for what
uTP is for: it defers to loss-based traffic where classic LEDBAT out-competes
it. (It no longer leaves less queue than classic LEDBAT when alone on a link;
see above.)

The default is nevertheless unchanged, for one reason: this library's standing
rule is to match libutp unless there is a concrete reason not to, and a
default is not the place to spend that. Switching it would change the
behaviour of every existing caller without their asking, and on a lossy
link it would cost them 60-75% of their throughput. It is one
line for a caller who wants it:

```go
config := utp.NewConnectionConfig()
config.CongestionAlgorithm = utp.AlgorithmLEDBATPP
```

**For a BitTorrent client -- the case in the brief -- that line should be
there.** Yielding to the user's own interactive traffic is the entire reason
uTP exists rather than plain TCP, and the numbers above say classic LEDBAT
does not do it: two thirds of a shared link taken from a loss-based flow, and
about 25 ms of queue at the bottleneck while it does. A client that ships classic LEDBAT is shipping
something that behaves like TCP with extra latency.

## Classic LEDBAT, before the M5 correction

The same suite at the previous commit, before the congestion controller was
corrected against libutp's `apply_ccontrol`. Both runs include every M3 and M4
fix, so the difference is the congestion control alone.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 82.83 Mbps | 81.56-84.88 | 405ms | 0.00% | 61508B | 98113B | 1.4% | 210µs | 836µs |
| Broadband (20ms, 10Mbps, no loss) | 4.47 Mbps | 4.38-4.47 | 1.878s | 0.00% | 30681B | 49300B | 0.5% | 348µs | 839µs |
| Broadband, 1% loss | 1.84 Mbps | 1.31-1.84 | 2.283s | 1.29% | 11404B | 22332B | 0.9% | 429µs | 880µs |
| Broadband, 5% loss | 1.02 Mbps | 0.69-1.02 | 2.063s | 4.77% | 6157B | 9896B | 1.4% | 485µs | 905µs |
| High BDP (100ms, 20Mbps, no loss) | 1.35 Mbps | 1.35-1.39 | 12.449s | 0.00% | 44866B | 68723B | 0.4% | 498µs | 1.039ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 3.11 Mbps | 3.10-3.19 | 1.349s | 0.00% | 21826B | 34504B | 1.0% | 206µs | 432µs |
| Reordering (20ms, 10Mbps, 2% reordered) | 1.76 Mbps | 1.74-1.86 | 2.377s | 1.70% | 10082B | 16217B | 0.6% | 541µs | 1.196ms |
| Two flows, 8Mbps bottleneck | 5.70 Mbps total | 5.51-5.72 | | | | | | | Jain 1.000 |

## What the M5 correction changed

The classic-LEDBAT table above is after seven defects were fixed against
libutp's `apply_ccontrol`. Medians, before and after:

| Profile | Before | After | |
| --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 82.83 Mbps | 89.65 Mbps | +8% |
| Broadband (20ms, 10Mbps) | 4.47 Mbps | 6.34 Mbps | +42% |
| Broadband, 1% loss | 1.84 Mbps | 3.27 Mbps | +78% |
| Broadband, 5% loss | 1.02 Mbps | 1.93 Mbps | +89% |
| High BDP (100ms, 20Mbps) | 1.35 Mbps | 2.09 Mbps | +55% |
| Shallow queue (16KB) | 3.11 Mbps | 4.70 Mbps | +51% |
| Reordering (2%) | 1.76 Mbps | 3.06 Mbps | +74% |
| Two flows, 8Mbps bottleneck | 5.70 Mbps total | 6.48 Mbps total | +14% |

The believed queueing delay went down as throughput went up, which is what
made this look unambiguously good at the time. The standing-queue column did
not exist yet. It does now, and it says classic LEDBAT is parking 33 ms on a
sustained transfer -- so the correct summary of the M5 correction is that it
made classic LEDBAT substantially faster, not that it made it well-behaved.

What each change was, and the libutp line it came from, is in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## What path-MTU discovery changed

The packet size is no longer a fixed 1024. It is discovered per connection by
binary search, between a 576-byte floor and a 1400-byte ceiling, and the
ceiling could only be raised because discovery makes it safe: an untested path
started at 988 bytes, *below* the old fixed size, and grew only once a probe
of a given size had been acknowledged. (It now starts at the ceiling, as
libutp's does; DEVIATIONS.md.) See
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

Against the fixed 1024 it replaces, on the profiles that resolve cleanly:

| Profile | fixed 1024 | discovered | |
| --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 89.80 Mbps | 89.21 Mbps | flat |
| Broadband (20ms, 10Mbps) | 6.35 Mbps | 6.40 Mbps | +0.8% |
| High BDP (100ms, 20Mbps) | 2.09 Mbps | 2.10 Mbps | flat |
| Shallow queue (16KB) | 4.71 Mbps | 4.75 Mbps | +0.8% |
| Long transfer (8MB) | 8.68 Mbps | 8.82 Mbps | +1.6% |
| Reordering (2%) | 3.08 Mbps | 3.40 Mbps | +10% |
| Two flows, 8Mbps | 6.46 Mbps total | 6.63 Mbps total | +2.6% |

The two loss profiles are omitted from this comparison on purpose: they were
lengthened in the same change, so their old and new numbers measure different
things.

The gains are modest because this emulator charges by the byte, so the only
saving is header overhead -- 20 bytes in 1024 against 20 in a discovered 1400.
On a real path the saving is larger, because per-packet cost is not purely
proportional to size. The reordering profile gains most for a different
reason: fewer, larger packets are fewer opportunities to be reordered.

## Against the reference implementation

Everything above compares this library to itself. This table compares it to
real libutp, run over the same emulated links by
`netem.TestLibutpOverEmulatedNetwork`: every sender against every receiver,
libutp against itself included. Seven repeats per cell, median with the
range, every transfer byte-verified, every flow timed from the moment before
the SYN to the receiver holding the last byte. Both ends run classic LEDBAT,
which is libutp's controller and this library's default.

| Profile | go->go | libutp->go | go->libutp | libutp->libutp |
| --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 88.43 (86.10-89.47) | 66.78 (16.73-88.28) | 88.02 (83.75-89.40) | 86.85 (21.70-87.80) |
| Broadband (20ms, 10Mbps) | 6.62 (6.25-6.63) | 5.51 (5.51-6.25) | 6.60 (6.55-6.64) | 5.51 (5.50-5.51) |
| Broadband, 1% loss | 4.31 (4.30-4.33) | 3.32 (3.32-3.33) | 4.33 (4.31-4.36) | 3.33 (3.32-4.13) |
| High BDP (100ms, 20Mbps) | 2.16 (2.15-2.16) | 1.95 (1.93-2.05) | 2.16 (2.15-2.16) | 2.03 (1.95-2.07) |
| Shallow queue (16KB) | 5.84 (4.14-5.89) | 5.51 (2.78-5.67) | 5.82 (4.16-5.89) | 5.51 (5.37-5.51) |

**The receiver makes little difference.** libutp's sender moves the same data
at the same rate into our receiver as into its own -- 5.51 Mbps either way on
broadband and on the shallow queue, 3.32 against 3.33 under loss, 1.95
against 2.03 on the high-BDP path with the ranges overlapping -- and ours
does the same into libutp's as into ours. So the differences across a row are
the sending controller's.

**Our sender is faster than libutp's**: 20% on broadband, 30% under 1% loss,
6-11% on the high-BDP path, 6% on the shallow queue -- more than when this
table was last regenerated (13%, 21%, 5% and 2%), most of it from the window
growing a round trip's worth per round trip (DEVIATIONS.md). That is not a win, and
reading it as one would be the mistake this file exists to prevent. LEDBAT's
whole purpose is to yield, and the deference measurement above already
records that our classic LEDBAT takes two thirds of a link from a loss-based
flow. A controller that yields less finishes sooner. The
defensible reading is that this fork's classic LEDBAT is more aggressive than
the reference's, which is a finding about this library.

An earlier version of this table said 34% and 41%, and that libutp's LAN runs
were bimodal. Both were the harness: a libutp sender was timed to libutp's
tearing the connection down, which it does only on one of its 500 ms timeout
passes, and against our receiver to our end of stream, which waited a round
trip for libutp's FIN. Timed to the last byte, both went away; the libutp
column rose from 4.19 to 5.51 Mbps on broadband.

**At 100 Mb/s libutp's sender is bimodal for either receiver,** which seven
runs cannot settle: twenty a side gave it 82.2 Mbps median into its own
receiver with 3 runs of 20 near 20 Mbps, and 88.8 into ours with none (two
near 66). Those twenty are after two harness fixes made on 2026-10-10: the
driver that runs libutp here moved its whole send queue on every write, and
our end read each datagram alone where libutp's end drains a batch; before
them, 79.1 and 74.3 Mbps, with 8 of 20 runs into ours near 20 Mbps
(KNOWN-LIMITATIONS.md).

The LAN column's low runs (the lows of 17-22 Mbps with libutp sending) were
shared protocol behaviour: before the loss probe every pairing had them, ours
to ours included (two runs in fifteen). Each was a fast retransmission lost in
the same full queue as the packet it replaced -- neither implementation
retransmitted a packet a second time before its timeout -- and a 1000 ms
timeout floor on a 1 ms path. Our classic-LEDBAT sender now has a loss probe,
and its two columns have none. Details
in [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), along with the three harness
defects that had to be fixed before this table meant anything.

## Two mechanisms, compared directly

The table above compares goodput. Goodput cannot tell two controllers apart
that reach the same rate by different routes, so these compare the mechanisms
themselves, against libutp, on the same links.

**The delay response.** The bottleneck is cut from 10 Mbps to 4 Mbps part-way
through a transfer, so a sender filling the old capacity is suddenly
overdriving the new one and a queue builds. What each controller settles at,
three runs each:

| | Standing queue at 10 Mbps | After the step to 4 Mbps |
| --- | --- | --- |
| libutp | 8.09 ms | 112.4 - 113.1 ms |
| ours | 10.48 ms | 117.6 - 118.4 ms |

Neither overflows the 256 KB queue, so both are reading the delay signal rather
than waiting for loss. The ranges do not overlap: this fork holds about 5 ms --
4% -- more queue than the reference, consistently, which is the same direction
as everything else here and the reason LEDBAT++ exists.

**Slow start.** Time to first carry 90% of an idle 10 Mbps link, 40 ms RTT:
libutp 588-640 ms, ours 536-660 ms. Overlapping, so no difference worth
claiming. Worth measuring anyway, because slow start was absent from this fork
entirely before M5.

An earlier version of the first test stepped the *propagation* delay and
asserted the sending rate fell. Both implementations passed, and it measured
nothing: tripling the RTT cuts the rate of any fixed window mechanically, so
the assertion held whatever the controller did. Converting the rates back into
windows showed both had in fact grown their window across that step --
correctly, since a permanent propagation change is not queueing and LEDBAT
relearns it as the new base delay.

## What the write copy cost

`UtpStream.Write` copies the caller's buffer before queueing it, because the
connection goroutine holds that slice after Write returns and the caller is
entitled to reuse it (see KNOWN-LIMITATIONS.md). One copy per Write call is
what that costs, and this is the measurement of it.

The LAN profile, where the copy should cost most: 100 Mbps, no loss, so more
bytes pass through it per second than on any other link here. Nine repeats per
cell, the two configurations run back to back on an otherwise idle machine.

| Controller | Without the copy | With the copy |
| --- | --- | --- |
| LEDBAT | 88.58 Mbps (86.45-91.62) | 89.46 (81.96-90.51) |
| LEDBAT++ | 77.13 Mbps (71.37-78.21) | 77.19 (73.10-77.96) |

No measurable cost: both medians move by less than 1%, in the *faster*
direction, and every difference is well inside the run-to-run range. That is
what the arithmetic predicts — the benchmark issues one Write for the whole
payload, so the copy is a single 4 MB memcpy against a 375 ms transfer.

**How not to measure this.** The first attempt compared a full suite run before
the change against a full suite run after it, and reported the change making
broadband goodput 80% faster and high-BDP goodput 52% slower. A memcpy does
neither. The two runs were an hour apart, and the earlier one came in far below
this file's own committed numbers for code that had not changed -- something
was competing with it. The same committed code gave the LAN profile 77.23 Mbps
in one full run and 89.46 in an isolated one. Cross-run comparisons in this
harness are only worth anything back to back; HARNESS.md says why, and this is
what ignoring it looks like.

## Two flows: the split while both ran

The two-flow rows in the tables above report Jain's index over each flow's
goodput, and that number cannot see an unfair split. Both flows carry the same
payload and start together, so whichever is squeezed finishes later, with the
link to itself for the last part, and its goodput catches up. A 60/40 split
while both ran still scores about 0.99. The rows now also report each flow's
share of the bytes delivered while both were running, and the link's
queue-overflow drops, and a second row runs the same two flows on a queue deep
enough that nothing is dropped.

Seven runs each, on the build that introduced the measurement:

| | Queue | Split while both ran | Worst | Queue drops | Jain |
| --- | --- | --- | --- | --- | --- |
| LEDBAT | 64KB (65ms) | 48/52 | 47.5/52.5 | 0 | 1.000 |
| LEDBAT | 2MB (2.1s) | 50/50 | 49.0/51.0 | 0 | 1.000 |
| LEDBAT++ | 64KB (65ms) | 36/64 | 34.2/65.8 | 8-10 | 0.916-0.948 |
| LEDBAT++ | 2MB (2.1s) | 20/80 or 34/66 | 17.8/82.2 | 0 | 0.926-0.970 |

Classic LEDBAT, the default, splits the link evenly on both queues and never
fills the shallow one. LEDBAT++ did not: about 36/64 on the shallow queue,
and on the deep one, with nothing dropped, worse, in two modes -- four runs
near 19/81 and three near 34/66. Its Jain figure in the table above, 0.999,
was taken from goodputs and did not show it.

That was four defects in LEDBAT++, since fixed (KNOWN-LIMITATIONS.md, "LEDBAT++
split a link unevenly between two of its own flows"): the ramp out of a
slowdown left on delay, a loss during a slowdown overwrote the window it was
returning to, the increase below the target was RFC 6817's rather than the
draft's, and a loss from before a slowdown was charged to the ramp, or charged
twice. After them, fifteen runs each:

| | Queue | Split while both ran | Worst | Queue drops |
| --- | --- | --- | --- | --- |
| LEDBAT++ | 64KB (65ms) | 49/51 | 45/55 | 9 |
| LEDBAT++ | 2MB (2.1s) | 49/51 | 47/53 | 0 |

`go test ./netem -run 'TestBenchmarkSuite/.*/Two_flows' -v` with
`UTP_BENCHMARK_REPEATS=7` reproduces it.

## What these numbers are not

- **Not a comparison against libutp.** The interoperability gate proves the
  two implementations talk to each other; it does not run libutp over this
  emulated network. Comparing congestion behaviour head to head would need the
  vendored libutp driven over the same links, which the harness cannot do yet.
  Until that exists, "matches libutp's congestion control" is a claim about
  code read against `utp_internal.cpp`, not a measurement.
- **Not a test against real TCP.** The competitor implements Reno's
  congestion control and nothing else of TCP: no header, no handshake, no
  SACK, no delayed acknowledgements. Its receiver acks every packet, where a
  real TCP receiver acks every second one, so it grows marginally faster than
  real TCP would -- which means a uTP flow measured against it is tested
  slightly harder than reality, the safe direction for a deference claim.
  Read the result as "against a loss-based sender that fills the queue", not
  as "against Linux TCP".
- **Not long enough to reach steady state on a high-BDP path** for classic
  LEDBAT. Its High BDP row is 2.09 Mbps on a 20 Mbps link because a window
  growing at 3000 bytes per round trip needs about 170 round trips to reach
  that path's bandwidth-delay product, and the transfer is over in 8 seconds.
  That is not a divergence from libutp -- libutp grows at exactly the same
  rate, `MAX_CWND_INCREASE_BYTES_PER_RTT` (`utp_internal.cpp:43`). It is the
  limitation LEDBAT++ addresses, and the High BDP row is where it shows.
- **Not a soak.** The longest transfer here is eleven seconds. LEDBAT++'s
  slowdown period grows with the measured slowdown duration, and nothing here
  runs long enough to exercise many cycles of that.
