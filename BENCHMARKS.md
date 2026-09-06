# Benchmarks

Every number here came from `go test ./netem -run TestBenchmarkSuite -v` and
`go test ./netem -run TestLatecomerShare -v`, on the emulated network
described in [HARNESS.md](HARNESS.md). Nothing in this file is estimated,
extrapolated, or carried over from a previous run.

## How to read them

Each link profile has a fixed seed, and the emulator's loss, reordering,
jitter and queueing all draw from it, so the *network* is identical between
runs. Our side is not: it runs on the real clock with real goroutines. Each
profile therefore runs three times and the **median** is reported, with the
full range beside it. Where the range is wide -- the 5% loss profile is the
one -- a single retransmission timeout costs a full RTO and dominates a short
transfer, and no amount of averaging hides that; the range is printed so it
cannot be mistaken for precision.

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
| LAN (1ms, 100Mbps, no loss) | 89.65 Mbps | 89.55-90.61 | 374ms | 0.00% | 96883B | 152622B | 0.7% | 209µs | 808µs | 5.467ms |
| Broadband (20ms, 10Mbps, no loss) | 6.34 Mbps | 6.21-6.35 | 1.323s | 0.00% | 50692B | 76238B | 0.3% | 193µs | 445µs | 2.048ms |
| Broadband, 1% loss | 3.27 Mbps | 3.10-3.28 | 1.281s | 1.34% | 21427B | 37362B | 0.5% | 422µs | 886µs | 1.139ms |
| Broadband, 5% loss | 1.93 Mbps | 0.97-1.93 | 1.088s | 4.99% | 13321B | 22984B | 0.9% | 297µs | 771µs | 1.181ms |
| High BDP (100ms, 20Mbps, no loss) | 2.09 Mbps | 2.09-2.09 | 8.017s | 0.00% | 72549B | 110444B | 0.2% | 501µs | 965µs | 952µs |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 4.70 Mbps | 4.70-4.71 | 893ms | 0.00% | 36643B | 55439B | 0.5% | 230µs | 388µs | 1.216ms |
| Long transfer (20ms, 10Mbps, 8MB) | 8.80 Mbps | 8.73-8.87 | 7.63s | 0.06% | 85446B | 113659B | 0.0% | 510µs | 912µs | 32.992ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 3.06 Mbps | 3.06-3.11 | 1.37s | 1.82% | 19150B | 28358B | 0.4% | 480µs | 19.874ms | 769µs |
| Two flows, 8Mbps bottleneck | 6.48 Mbps total | 6.46-6.48 | | | | | | | | Jain 1.000 |

## LEDBAT++

Opt in with `ConnectionConfig.CongestionAlgorithm = AlgorithmLEDBATPP`.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 | standing queue p50 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 74.22 Mbps | 71.94-75.02 | 452ms | 0.00% | 104991B | 263900B | 0.6% | 234µs | 977µs | 4.227ms |
| Broadband (20ms, 10Mbps, no loss) | 2.91 Mbps | 2.82-2.93 | 2.884s | 2.26% | 36134B | 139225B | 5.9% | 298µs | 727µs | 1.175ms |
| Broadband, 1% loss | 0.92 Mbps | 0.92-1.12 | 4.539s | 1.26% | 8344B | 36519B | 8.9% | 438µs | 1.007ms | 852µs |
| Broadband, 5% loss | 0.51 Mbps | 0.49-0.53 | 4.108s | 4.88% | 3129B | 5582B | 9.4% | 424µs | 982µs | 685µs |
| High BDP (100ms, 20Mbps, no loss) | 4.09 Mbps | 4.09-4.09 | 4.102s | 0.00% | 252974B | 479261B | 22.2% | 395µs | 881µs | 7.698ms |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 2.07 Mbps | 2.06-2.12 | 2.022s | 3.62% | 26283B | 85459B | 5.9% | 235µs | 631µs | 2.233ms |
| Long transfer (20ms, 10Mbps, 8MB) | 6.22 Mbps | 6.03-6.27 | 10.791s | 0.23% | 43871B | 139361B | 2.4% | 579µs | 1.104ms | 1.551ms |
| Reordering (20ms, 10Mbps, 2% reordered) | 1.00 Mbps | 0.94-1.00 | 4.205s | 1.03% | 5866B | 21536B | 5.2% | 529µs | 2.041ms | 866µs |
| Two flows, 8Mbps bottleneck | 3.66 Mbps total | 3.63-3.67 | | | | | | | | Jain 0.997 |

## What LEDBAT++ costs, and what it buys

Read as a throughput table, LEDBAT++ looks like a regression: it is slower on
six of eight profiles, by as much as a factor of three. That reading is
incomplete, and the two rows that matter tell the real story.

**It wins where the window has to get large.** High BDP (100 ms, 20 Mbps):
2.09 -> 4.09 Mbps, mean congestion window 72 KB -> 253 KB. Classic LEDBAT
grows at a flat 3000 bytes per round trip whatever the path, so on a long fat
link it never reaches the bandwidth-delay product; LEDBAT++'s gain scales with
the base RTT (§4.2) and its slow start is exponential, so it does.

**It leaves the path alone.** On the sustained 8 MB transfer:

| | Goodput | Standing queue p50 |
| --- | --- | --- |
| LEDBAT | 8.80 Mbps | **32.99 ms** |
| LEDBAT++ | 6.22 Mbps | **1.55 ms** |

29% less throughput for 21x less queue. On the deeper-queue latecomer link
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
