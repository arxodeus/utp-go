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
profile therefore runs three times and the **median** is reported, with the
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
| LAN (1ms, 100Mbps, no loss) | 89.21 Mbps | 85.56-91.21 | 376ms | 0.00% | 97283B | 153045B | 0.7% | 193µs | 740µs | 5.637ms |
| Broadband (20ms, 10Mbps, no loss) | 6.40 Mbps | 6.37-6.41 | 1.312s | 0.00% | 51098B | 76359B | 0.3% | 253µs | 459µs | 2.433ms |
| Broadband, 1% loss | 5.22 Mbps | 3.91-5.25 | 6.425s | 0.72% | 32602B | 62149B | 0.1% | 517µs | 1.009ms | 1.454ms |
| Broadband, 5% loss | 1.24 Mbps | 0.98-1.59 | 13.498s | 6.44% | 14388B | 24910B | 1.1% | 462µs | 1.029ms | 1.448ms |
| High BDP (100ms, 20Mbps, no loss) | 2.10 Mbps | 2.10-2.10 | 8.004s | 0.00% | 73141B | 110611B | 0.3% | 441µs | 977µs | 1.058ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 4.75 Mbps | 4.69-4.77 | 882ms | 0.00% | 36773B | 55483B | 0.6% | 177µs | 472µs | 1.65ms |
| Long transfer (20ms, 10Mbps, 8MB) | 8.82 Mbps | 8.66-8.91 | 7.608s | 0.05% | 84619B | 114121B | 0.0% | 366µs | 796µs | 32.263ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 3.40 Mbps | 3.36-3.70 | 1.234s | 1.89% | 21385B | 32393B | 0.5% | 391µs | 1.339ms | 1.054ms |
| Two flows, 8Mbps bottleneck | 6.63 Mbps total | 6.55-6.65 | | | | | | | | Jain 1.000 |

## LEDBAT++

Opt in with `ConnectionConfig.CongestionAlgorithm = AlgorithmLEDBATPP`.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 | standing queue p50 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 77.01 Mbps | 76.56-77.20 | 436ms | 0.00% | 108966B | 262201B | 0.6% | 256µs | 973µs | 4.949ms |
| Broadband (20ms, 10Mbps, no loss) | 3.45 Mbps | 1.79-3.58 | 2.432s | 2.40% | 41878B | 140769B | 7.3% | 228µs | 525µs | 2.438ms |
| Broadband, 1% loss | 1.51 Mbps | 1.37-1.54 | 22.241s | 0.78% | 9350B | 47660B | 5.8% | 563µs | 1.083ms | 883µs |
| Broadband, 5% loss | 0.54 Mbps | 0.45-0.57 | 31.275s | 5.19% | 4145B | 9213B | 12.4% | 525µs | 1.066ms | 1.131ms |
| High BDP (100ms, 20Mbps, no loss) | 4.38 Mbps | 4.37-4.38 | 3.834s | 0.00% | 294953B | 577090B | 23.4% | 327µs | 849µs | 24.554ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 2.33 Mbps | 1.23-2.39 | 1.799s | 3.47% | 27243B | 85219B | 7.9% | 246µs | 659µs | 2.356ms |
| Long transfer (20ms, 10Mbps, 8MB) | 6.85 Mbps | 6.81-6.94 | 9.802s | 0.23% | 51102B | 141151B | 2.8% | 551µs | 1.108ms | 4.759ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 1.31 Mbps | 0.94-2.31 | 3.195s | 1.23% | 8200B | 28056B | 4.9% | 460µs | 1.098ms | 933µs |
| Two flows, 8Mbps bottleneck | 4.02 Mbps total | 3.94-5.38 | | | | | | | | Jain 0.999 |

## What LEDBAT++ costs, and what it buys

Read as a throughput table, LEDBAT++ looks like a regression: it is slower on
six of eight profiles, by as much as a factor of three. That reading is
incomplete, and the two rows that matter tell the real story.

**It wins where the window has to get large.** High BDP (100 ms, 20 Mbps):
2.10 -> 4.38 Mbps, mean congestion window 73 KB -> 295 KB. Classic LEDBAT
grows at a flat 3000 bytes per round trip whatever the path, so on a long fat
link it never reaches the bandwidth-delay product; LEDBAT++'s gain scales with
the base RTT (§4.2) and its slow start is exponential, so it does.

**It leaves the path alone.** On the sustained 8 MB transfer:

| | Goodput | Standing queue p50 |
| --- | --- | --- |
| LEDBAT | 8.82 Mbps | **32.26 ms** |
| LEDBAT++ | 6.85 Mbps | **4.76 ms** |

22% less throughput for 7x less queue. On the deeper-queue latecomer link
(256 KB, ~200 ms of buffering) the gap is wider still: 38.1 ms against 1.1 ms.

That is the entire proposition of a "less than best effort" protocol, and it
is the one thing uTP exists to provide. Classic LEDBAT as libutp implements it
does not provide it: 33 ms of standing queue is not deference, it is
bufferbloat with extra steps. It is fast *because* it is not yielding.

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

| | uTP | Competitor | Competitor kept | uTP's share | Bottleneck queue p50 |
| --- | --- | --- | --- | --- | --- |
| LEDBAT | 5.79 Mbps | 3.94 Mbps | 69% | **60%** | **24.6 ms** |
| LEDBAT++ | 3.37 Mbps | 4.34 Mbps | 77% | **44%** | **2.5 ms** |

Classic LEDBAT does not yield. It takes 60% of a shared link from a
loss-based flow -- more than an equal share, against the traffic it is
supposed to defer to -- and costs that flow nearly a third of its throughput,
while leaving 25 ms of queue for anything else on the path.

LEDBAT++ takes 44%, leaves the competitor 77% of what it had alone, and
leaves a tenth of the queue.

This is the measurement that was missing, and it reverses the reading of the
throughput tables. Classic LEDBAT is faster in those tables *because it is not
doing the one thing the protocol exists to do*.

### The latecomer experiment

`TestLatecomerShare`. One uTP flow runs until it has filled the bottleneck
queue; a second joins 1.5 s later, so every delay sample it will ever take
begins against a full queue. Its idea of the empty path is wrong from its
first packet -- the failure RFC 6817 acknowledges and LEDBAT++ §4.4 answers.

| | Incumbent | Latecomer | Ratio | Jain |
| --- | --- | --- | --- | --- |
| LEDBAT | 7.30 Mbps | 5.69 Mbps | 0.78 | 0.985 |
| LEDBAT++ | 3.77 Mbps | 3.89 Mbps | 1.03 | 1.000 |

LEDBAT++ splits the link evenly and classic LEDBAT does not, but the honest
reading is that this difference is small (0.985 against 1.000) next to the
aggregate throughput difference (12.99 Mbps against 7.66). Between two uTP
flows, classic LEDBAT's unfairness costs less than LEDBAT++'s deference does.
The deference experiment above is the one that decides the question, because
competing with itself is not what uTP is for.

### Why the default is still classic LEDBAT

On the evidence above, LEDBAT++ is the better congestion controller for what
uTP is for. It defers to loss-based traffic where classic LEDBAT out-competes
it, and it leaves a tenth of the queue.

The default is nevertheless unchanged, for one reason: this library's standing
rule is to match libutp unless there is a concrete reason not to, and a
default is not the place to spend that. Switching it would change the
behaviour of every existing caller without their asking, and on an
uncontended link it would cost some of them half their throughput. It is one
line for a caller who wants it:

```go
config := utp.NewConnectionConfig()
config.CongestionAlgorithm = utp.AlgorithmLEDBATPP
```

**For a BitTorrent client -- the case in the brief -- that line should be
there.** Yielding to the user's own interactive traffic is the entire reason
uTP exists rather than plain TCP, and the numbers above say classic LEDBAT
does not do it: 60% of a shared link taken from a loss-based flow, and 25 ms
of self-inflicted queue. A client that ships classic LEDBAT is shipping
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
starts at 988 bytes, *below* the old fixed size, and grows only once a probe
of a given size has been acknowledged. See
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
`netem.TestLibutpOverEmulatedNetwork`. Seven repeats per cell, median with the
range, every transfer byte-verified. Both ends run classic LEDBAT, which is
libutp's controller and this library's default.

| Profile | go->go | libutp->go | go->libutp |
| --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 86.78 (84.10-88.95) | 33.50 (16.65-66.69) | 83.43 (72.99-90.57) |
| Broadband (20ms, 10Mbps) | 6.39 (6.28-6.40) | 4.19 (4.19-4.19) | 6.43 (6.21-6.45) |
| Broadband, 1% loss | 3.94 (3.91-4.05) | 2.80 (2.79-3.35) | 3.87 (2.69-4.06) |
| High BDP (100ms, 20Mbps) | 2.10 (2.08-2.10) | 1.86 (1.86-1.86) | 2.09 (2.07-2.10) |
| Shallow queue (16KB) | 5.62 (3.29-5.66) | 4.19 (3.35-4.19) | 5.56 (3.30-5.66) |

Our sender is faster than libutp's everywhere — 34% on broadband, 41% under 1%
loss. That is not a win, and reading it as one would be the mistake this file
exists to prevent. LEDBAT's whole purpose is to yield, and the LEDBAT++ section
above already records that our classic LEDBAT leaves 33 ms of standing queue on
a 40 ms path where LEDBAT++ leaves 1.55 ms. A controller that yields less
finishes sooner. The defensible reading is that this fork's classic LEDBAT is
more aggressive than the reference's, which is a finding about this library.

The `go->libutp` column is the useful control: our sender against libutp's
receiver lands within noise of our sender against our own receiver on every
profile, so the difference in the middle column is the sending controller and
not the receiving end.

libutp's LAN figure is bimodal — twelve consecutive runs came in at either
~505 ms or ~2.003 s — and the gap is one retransmission timeout against its
1000 ms RTO floor and 500 ms check granularity. Our seven runs showed no such
mode. Details in [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), along with the
two harness defects that had to be fixed before this table meant anything.

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
