# The network harness (M1)

`netem` is an in-process emulated network for measuring uTP under conditions
loopback cannot produce.

It exists because a congestion controller that passes unit tests tells you
nothing. Too aggressive and it floods while claiming to be a background
transport; too timid and it gets near-zero throughput. Against loopback both
look like "it works". Every congestion result in this repository that is not
measured here should be treated as unmeasured.

## What it does

`Endpoint` satisfies `utp_go.Conn` (`ReadFrom`/`WriteTo`/`Close`), so it drops
straight into `utp_go.WithSocket` in place of a UDP socket. No root, no
network namespaces, no external tooling — which is a genuine advantage of this
codebase's `Conn` interface being only three methods.

Per direction, independently configurable:

| Setting | Meaning |
| --- | --- |
| `Delay` | one-way propagation delay |
| `Jitter` | uniform perturbation of the delay, `+/- Jitter` |
| `LossRate` | independent per-packet drop probability on the medium |
| `ReorderRate` / `ReorderDelay` | probability a packet is displaced, and by how much |
| `BandwidthBps` | bottleneck rate; packets are serialized at it, so flows contend |
| `QueueBytes` | bottleneck queue; overflow is tail-dropped as congestion loss |
| `Seed` | per-link PRNG seed |

`Network.SetConfig` changes conditions mid-flight, for recovery and soak
tests. `ConnectAsymmetric` gives each direction its own conditions, because
real paths are rarely symmetric.

## What it measures

**Link level** (`Link.Stats`, `Link.QueueSamples`, `Link.QueueDelayPercentile`):
packets and bytes offered, delivered and dropped, drops broken down by cause
(medium loss, queue overflow, receiver overflow), packets reordered, and
queueing delay as mean, max, percentiles and a time series.

Drop causes are kept separate on purpose. Congestion loss and medium loss mean
different things to a controller, and a harness that reported only a total
would hide which one it was reacting to.

**Connection level** (`Recorder`, fed by `utp_go.ConnectionConfig.Metrics`):
congestion window, bytes in flight, RTT estimate, retransmission timeout, base
delay, measured one-way delay, retransmissions, timeouts, fast retransmits and
buffer occupancy — sampled over time and summarised as means and percentiles.

`Recorder.Summary` reports the numbers the M5 gates are stated in, including
`CwndPinnedAtMin`, the fraction of samples where the window sat at its floor.
A controller stuck there has collapsed rather than backed off, and average
throughput will not tell you which happened.

**Flow level** (`UtpPair`, `RunTransfer`): goodput, elapsed time, payload
verification, and per-side recorders. `FairnessIndex` reports Jain's index for
a set of flow rates — the standard way to state how flows shared a bottleneck.

## Determinism

Every random decision is drawn from a per-link PRNG seeded from the network
seed, so a single flow over a link makes the same decisions every run.
`TestSameSeedReproducesSameDrops` asserts this by comparing the exact
delivered sequence across two runs.

Two limits, stated because a harness that overclaims is worse than none:

- **Delivery uses the real clock.** Scheduling decisions reproduce; the
  wall-clock instants of arrival are subject to Go scheduler noise. Making
  time virtual would mean threading an injectable clock through the whole
  library, which the library does not currently support.
- **Concurrent flows sharing a link race for the PRNG.** Per-link aggregate
  behaviour is stable; per-flow drop sequences with several senders are not.

Assertions in the harness's own tests are written against distributions and
invariants, not against extremes, for this reason. A congestion test that
flakes gets deleted, and then nothing is tested.

## The M1 gate

The brief's gate is a self-test proving the harness is sound. All measured on
loopback-free in-process links:

| Property | Configured | Observed |
| --- | --- | --- |
| Bandwidth | 2 Mbps | 2.00 Mbps (ratio 1.000) |
| Bandwidth | 10 Mbps | 10.00 Mbps (ratio 1.000) |
| One-way delay | 20 ms | 20.29 ms mean |
| One-way delay | 100 ms | 100.39 ms mean |
| Round trip (25 ms each way) | 50 ms | 50.80 ms mean |
| Loss | 1% | 1.033% |
| Loss | 5% | 5.033% |
| Reordering | 10% | 10.45%, with real inversions observed |
| Jitter | 40 ms ± 10 ms | min 30.2 ms, mean 41.6 ms, within bounds |
| Queue ceiling (16 KiB @ 4 Mbps) | 32.77 ms | 30.3 ms max observed |
| Two greedy flows, 8 Mbps link | even split | 8.00 Mbps total, Jain 0.999 |

Bandwidth is measured in steady state, after the queue has filled. Measuring
across the fill or the drain would overstate the rate; that is a measurement
artifact, not a link property, and getting it wrong was the first thing this
gate caught.

The delay figures sit a few hundred microseconds above the configured value
because the emulator can only ever *add* delay. That excess is scheduler
wake-up latency, and it is the harness's noise floor: do not read
sub-millisecond delay differences as signal.

## Running it

```sh
go test ./netem/            # the gate
go test -race ./netem/
```

To measure a flow:

```go
n := netem.NewNetwork(seed)
defer n.Close()
a, b := n.MustAddEndpoint("sender"), n.MustAddEndpoint("receiver")
n.Connect(a, b, netem.Config{
    Delay:        20 * time.Millisecond,
    BandwidthBps: 8_000_000,
    QueueBytes:   48 * 1024,
})

pair := netem.NewUtpPair(ctx, n, a, b, logger)
res, err := pair.RunTransfer(ctx, payload, netem.FlowOptions{
    InitiatorCid:    100,
    MetricsInterval: 2 * time.Millisecond,
    Verify:          true,
})

fmt.Println(res.Goodput)              // application goodput
fmt.Println(res.Sender.Summary())     // cwnd, RTT, queueing delay, retransmits
fmt.Println(n.Link("sender", "receiver").Stats())
```

## What it has found so far

Two defects, both invisible to the unit tests and to loopback, both caught
within minutes of the harness first carrying a flow:

- **The RTT estimate tracked nothing** -- it converged to about 5 µs on every
  path, because `OnAck` mixed milliseconds, microseconds and nanoseconds in
  two lines. Fixed; a 40 ms path now estimates 43.1 ms.
- **6% of packets were retransmitted on a link that drops nothing**, because
  the retransmission timer wheel could not represent an RTO shorter than its
  one-second tick. Fixed by sharing one 25 ms wheel across the socket;
  measured 0.00% after, with goodput up 53% on the emulated path.

Both are written up in [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md). Neither
would have been visible in a throughput number alone, which is the argument
for recording cwnd and RTT rather than just goodput.

## Shared bottlenecks, and a loss-based competitor

Two things were added so the M5 deference gate could be run at all.

**`Network.ConnectShared(cfg, pairs...)`** routes several endpoint pairs
through one `Link`: one queue, one rate, one loss draw. `Connect` and
`ConnectAsymmetric` give every directed pair its own link, so two flows
between different endpoint pairs each got their own queue and their own full
bandwidth -- they ran alongside each other without ever contending. No
congestion controller can be judged against another on links like that,
because neither can crowd the other out.

**`netem.RunRenoFlow(ctx, sender, receiver, bytes, mss)`** is a loss-based
competitor: TCP Reno's congestion control exactly -- slow start, one segment
per round trip in congestion avoidance, fast retransmit on three duplicate
acknowledgements, fast recovery, RFC 6298 timers with Karn's algorithm -- and
none of TCP's protocol.

It is emphatically not TCP, and `netem/reno.go` opens by saying what it does
and does not model: no header, no handshake, no options, no SACK, no Nagle,
no delayed acknowledgements, no interoperability with anything. Its receiver
acknowledges every packet where a real one acknowledges every second, so it
grows marginally faster than real TCP. That error is in the safe direction
for the claim being tested: a uTP flow measured against it is tested slightly
harder than reality.

Results are in [BENCHMARKS.md](BENCHMARKS.md). The short version is that
classic LEDBAT takes 60% of a shared bottleneck from this competitor, which is
the opposite of what the protocol is for.

## Real libutp as an endpoint

`libutp_flow_test.go` puts the reference implementation on one end of an
emulated link. `libutp.Driver` is built for this without knowing it: no socket,
no clock, no threads, everything through explicit calls. The loop supplies all
three — it takes packets off a netem `Endpoint` and injects them, sets libutp's
clock from the same `time.Now().UnixMicro()` base this library stamps packets
with, calls `utp_issue_deferred_acks` after each batch and `utp_check_timeouts`
on a 200µs tick, drains received data every pass so libutp's advertised window
stays open, and writes whatever it emits back onto the link.

Exactly one goroutine touches the driver, because the driver is explicitly not
safe for concurrent use. Packets reach it through a channel filled by a reader
goroutine that never touches it.

Two gates use it: `TestLibutpTransfersOverEmulatedNetwork`, which is cheap and
runs every time, and `TestLibutpOverEmulatedNetwork`, which produces the
comparison table in [BENCHMARKS.md](BENCHMARKS.md) and honours
`UTP_BENCHMARK_REPEATS`.

The cheap gate matters more than its throughput number suggests. Wiring the
reference up wrongly is easy and does not look like an error — it looks like a
result. Two defects in this harness were found by measuring rather than by
reading, and both made libutp look worse than it is; they are in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## A fairness test that was measuring the Go scheduler

`TestTwoFlowsShareBottleneck` asserts a property of the link model: given an
evenly contended queue, a FIFO should deliver an even split. It took two
attempts to make it measure that.

Its comment said each flow offered "~75% of the link". Each actually offered
50 packets every 5ms, which against an 8 Mbps link carrying 1000-byte payloads
is ten times the whole link rate -- 95% of everything offered was dropped. At
that load the queue is permanently full, so which flow gets a slot is decided
by which goroutine the Go scheduler happened to wake. The test passed 3 times
out of 3 alone and failed at Jain 0.885 -- 639 packets against 1361 -- inside a
full-package run.

Cutting the burst to 4 packets, about 80% of the link per flow, made it much
better and **not** reliable: six runs under heavy load came back between 0.988
and 1.000, which looked like a fix, and it was not. Eight further runs later
produced 0.826. Six samples were not enough to tell a narrowed distribution
from a fixed one.

The remaining variance was structural. What each goroutine manages to *offer*
is itself decided by the scheduler, so Jain computed over delivered counts
conflates the queue's fairness with the scheduler's, however modest the offered
load. One feeder goroutine alternating between the two flows removes that
term rather than shrinking it: the queue sees a perfectly interleaved arrival
pattern, and what comes out the other end is the link's doing.

Measured since: 0.985 to 0.994 across ten ordinary runs, and 0.981 to 0.999
across twelve under six CPU burners on four cores with the rest of the suite
running alongside. The slight, consistent lean toward the first flow is real
and not noise -- it is written first within each tick, so it wins the last slot
before the queue fills.

It is worth saying what this was never about: the link model was not unfair.
The test was reporting on the machine it ran on and attributing the answer to
the code.

## A link that can refuse a packet for its size

`Config.MTU` drops datagrams larger than the path will carry, as an IPv6 path
does and as IPv4 does for a packet marked don't-fragment. Its drops are counted
as `DroppedByMTU`, apart from medium loss and queue overflow, because they mean
something different: a statement about size, carrying no information about
congestion.

It exists because path-MTU discovery had never been tested against a path that
limits size. Every link here carried any datagram however large, so the search
climbed to whatever ceiling it was given, every time, and the half of it that
lowers the ceiling was never exercised end to end. The harness could not
disagree with the code.

The first run against it found two defects and one limitation that turned out
to be libutp's as well -- see [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).
Telling those apart was only possible because the reference could be run over
the same link.

## What it does not do

- **The competitor is Reno-shaped, not TCP.** Read a result from it as
  "against a loss-based sender that fills the queue". Testing against real
  kernel TCP would need a real bottleneck and real sockets.
- **No virtual time**, per above.
- **Bandwidth is modelled as pure serialization** with a tail-drop FIFO.
  There is no AQM, no token bucket burst allowance, and no per-flow queueing.
  A real bottleneck may do any of those.
- **Loss is independent per packet.** Real loss is bursty. `ReorderRate` and
  `Jitter` add structure, but this is not a substitute for a burst-loss model.
