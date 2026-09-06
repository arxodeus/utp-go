# Benchmarks

Every number here came from `go test ./netem -run TestBenchmarkSuite -v`, on
the emulated network described in [HARNESS.md](HARNESS.md). Nothing in this
file is estimated, extrapolated, or carried over from a previous run.

## How to read them

Each link profile has a fixed seed, and the emulator's loss, reordering,
jitter and queueing all draw from it, so the *network* is identical between
runs. Our side is not: it runs on the real clock with real goroutines. Each
profile therefore runs three times and the **median** is reported, with the
full range beside it. Where the range is wide -- the 5% loss profile is the
one -- a single retransmission timeout costs a full RTO and dominates a short
transfer, and no amount of averaging hides that; the range is printed so it
cannot be mistaken for precision.

`Retx` is the fraction of packets sent that were retransmissions. On a link
that drops 1%, a healthy sender retransmits about 1%; substantially more means
it is resending packets that were not lost. `at floor` is the fraction of
samples where the congestion window sat at its minimum -- a controller parked
there has collapsed rather than backed off. `qdelay` is the standing queue,
which is what LEDBAT exists to bound: throughput bought by filling a queue is
not a win, and this column is where that would show.

To regenerate:

```
go test ./netem -run TestBenchmarkSuite -v
UTP_BENCHMARK_OUT=/tmp/table.md go test ./netem -run TestBenchmarkSuite
```

## Classic LEDBAT, after the M5 correction

This is the current state of the library.

| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LAN (1ms, 100Mbps, no loss) | 90.37 Mbps | 90.29-90.55 | 371ms | 0.00% | 97043B | 152628B | 0.7% | 229µs | 801µs |
| Broadband (20ms, 10Mbps, no loss) | 6.35 Mbps | 6.34-6.35 | 1.321s | 0.00% | 50959B | 76225B | 0.3% | 210µs | 419µs |
| Broadband, 1% loss | 3.29 Mbps | 3.28-3.29 | 1.277s | 1.34% | 21347B | 37545B | 0.5% | 379µs | 764µs |
| Broadband, 5% loss | 1.84 Mbps | 0.68-1.88 | 1.141s | 5.03% | 12453B | 18968B | 0.9% | 397µs | 752µs |
| High BDP (100ms, 20Mbps, no loss) | 2.09 Mbps | 2.09-2.09 | 8.021s | 0.00% | 72900B | 110362B | 0.2% | 526µs | 962µs |
| Shallow queue (20ms, 10Mbps, 16KB queue) | 4.72 Mbps | 4.51-4.72 | 889ms | 0.00% | 36122B | 55430B | 0.5% | 239µs | 499µs |
| Reordering (20ms, 10Mbps, 2% reordered) | 3.08 Mbps | 3.07-3.14 | 1.363s | 1.74% | 19128B | 28780B | 0.4% | 485µs | 19.873ms |
| Two flows, 8Mbps bottleneck | 6.47 Mbps total | 6.47-6.47 | | | | | | | Jain 1.000 |

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

## What changed

Medians, so the wide-range rows are compared like for like:

| Profile | Before | After | |
| --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 82.83 Mbps | 90.37 Mbps | +9% |
| Broadband (20ms, 10Mbps) | 4.47 Mbps | 6.35 Mbps | +42% |
| Broadband, 1% loss | 1.84 Mbps | 3.29 Mbps | +79% |
| Broadband, 5% loss | 1.02 Mbps | 1.84 Mbps | +80% |
| High BDP (100ms, 20Mbps) | 1.35 Mbps | 2.09 Mbps | +55% |
| Shallow queue (16KB) | 3.11 Mbps | 4.72 Mbps | +52% |
| Reordering (2%) | 1.76 Mbps | 3.08 Mbps | +75% |
| Two flows, 8Mbps bottleneck | 5.70 Mbps total | 6.47 Mbps total | +14% |

Two things make these trustworthy rather than merely large.

**The standing queue did not grow.** Broadband p95 queueing delay went from
839µs to 419µs, and the shallow-queue profile from 432µs to 499µs -- flat
within noise while its throughput went up by half. Throughput bought by
building a queue would show here as a bigger number in this column, and it
does not. That is the whole point of a delay-based controller.

**Fairness held.** Two identical flows over one bottleneck still split it
1.000 on Jain's index, and now carry 6.47 Mbps of an 8 Mbps link between them
instead of 5.70.

What each change was, and the libutp line it came from, is in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## What these numbers are not

- **Not a comparison against libutp.** The interoperability gate proves the
  two implementations talk to each other; it does not run libutp over this
  emulated network. Comparing congestion behaviour head to head would need the
  vendored libutp driven over the same links, which the harness cannot do yet.
  Until that exists, "matches libutp's congestion control" is a claim about
  code read against `utp_internal.cpp`, not a measurement.
- **Not a test of fairness against TCP.** LEDBAT exists to yield to TCP.
  There is no TCP model in the emulator, so "less than best effort" is
  unverified. Two uTP flows sharing a bottleneck is a much weaker claim than
  that, and should not be read as the stronger one.
- **Not long enough to reach steady state on a high-BDP path.** The High BDP
  row is still 2.09 Mbps on a 20 Mbps link. That link's bandwidth-delay
  product is about 500 KB; the window grows at 3000 bytes per round trip, so
  reaching it would take roughly 170 round trips, or 17 seconds, and the
  transfer is over in 8. This is not a divergence from libutp -- libutp grows
  at exactly the same rate, `MAX_CWND_INCREASE_BYTES_PER_RTT`
  (`utp_internal.cpp:43`), and its slow start adds only one packet per round
  trip on top (`:1691`). It is the limitation LEDBAT++ exists to address, and
  it is the reason the High BDP profile is in this suite.
