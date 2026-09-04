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

## What it does not do

- **No TCP model.** The M5 gate "yields to a greedy TCP flow" cannot be run
  against this as it stands. It needs either a TCP model over the same
  bottleneck or a real kernel TCP flow through a real bottleneck.
- **No virtual time**, per above.
- **Bandwidth is modelled as pure serialization** with a tail-drop FIFO.
  There is no AQM, no token bucket burst allowance, and no per-flow queueing.
  A real bottleneck may do any of those.
- **Loss is independent per packet.** Real loss is bursty. `ReorderRate` and
  `Jitter` add structure, but this is not a substitute for a burst-loss model.
