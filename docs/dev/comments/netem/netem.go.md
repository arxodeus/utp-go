# netem/netem.go

Every comment in `netem/netem.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
// Package netem provides an in-process emulated network for testing uTP.
//
// It exists because a congestion controller that passes unit tests tells you
// nothing. Too aggressive and it floods while claiming to be a background
// transport; too timid and it gets near-zero throughput. Against loopback both
// look like "it works". This package makes the difference measurable.
//
// An Endpoint satisfies the utp.Conn interface (ReadFrom/WriteTo/Close), so
// it drops straight into utp.WithSocket in place of a UDP socket. No root,
// no network namespaces, no external tooling.
//
// # Determinism
//
// Every random decision -- loss, jitter, reordering -- is drawn from a
// per-link PRNG seeded from Config.Seed, so a single flow over a link makes
// exactly the same decisions on every run. Two runs of the same test drop the
// same packets.
//
// Two caveats, stated plainly because a harness that overclaims is worse than
// none:
//
//   - Delivery uses the real clock. Packet-scheduling decisions are
//     reproducible, but the wall-clock instants at which packets actually
//     arrive are subject to Go scheduler noise. Making time itself virtual
//     would require threading an injectable clock through the whole library.
//   - When several flows share one link, the order in which their WriteTo
//     calls reach the link is genuinely concurrent, so which flow's packet
//     draws which PRNG value can vary between runs. Per-link aggregate
//     behaviour is stable; per-flow drop sequences are not.
```

## Line 42 — var _

```
// Endpoint must be usable anywhere a real UDP socket is.
```

## Line 46 — var ErrClosed, ErrNoRoute, ErrBufferTooSmall

```
// ErrClosed is returned by ReadFrom and WriteTo after Close.
```

## Line 48 — var ErrClosed, ErrNoRoute, ErrBufferTooSmall

```
// ErrNoRoute is returned when writing to an endpoint with no link.
```

## Line 50 — var ErrClosed, ErrNoRoute, ErrBufferTooSmall

```
// ErrBufferTooSmall is returned when a read buffer cannot hold the packet.
```

## Line 54 — type Config

```
// Config describes one direction of an emulated path. The zero value is a
// perfect link: no delay, no loss, unlimited bandwidth.
```

## Line 57 — type Config

```
// Delay is the one-way propagation delay.
```

## Line 60 — type Config

```
// Jitter perturbs Delay by a uniform amount in [-Jitter, +Jitter].
	// Delivery order is preserved unless Jitter exceeds packet spacing.
```

## Line 64 — type Config

```
// LossRate is the independent probability of dropping a packet, in [0,1].
	// This is loss on the medium; it is counted separately from queue
	// overflow, which is loss caused by congestion.
```

## Line 69 — type Config

```
// DuplicateRate is the probability that a packet is delivered twice, in
	// [0,1]. The copy is made past the bottleneck, as a link-layer
	// retransmission or a misbehaving switch would make it: it takes no
	// queue space and arrives just after the original.
```

## Line 75 — type Config

```
// ReorderRate is the probability that a packet is displaced backwards in
	// the delivery order by ReorderDelay, in [0,1].
```

## Line 79 — type Config

```
// ReorderDelay is how much extra delay a reordered packet receives.
	// Defaults to DefaultReorderDelay.
```

## Line 83 — type Config

```
// BandwidthBps is the bottleneck rate in bits per second. Zero means
	// unlimited: packets are not serialized and never queue.
	//
	// Packets are serialized at this rate, so a packet's transmission
	// occupies len*8/BandwidthBps seconds during which no other packet on
	// this link can be sent. Flows sharing a link contend for it.
```

## Line 91 — type Config

```
// QueueBytes is the bottleneck queue capacity. A packet arriving when the
	// queue is full is tail-dropped. Defaults to DefaultQueueBytes.
	//
	// This is what bounds standing queueing delay, and therefore what a
	// delay-based controller is supposed to keep clear of.
```

## Line 98 — type Config

```
// NoQueueSamples stops the link recording a QueueSample for every
	// packet. The samples are what QueueSamples reports, for the benchmarks'
	// standing-queue figures, and they are kept for the link's life: 40 bytes
	// a packet, which a soak of hours turns into gigabytes of the process's
	// heap -- 2.5 GB in two hours, all of it the emulator's, which is what
	// the soak then measured.
```

## Line 106 — type Config

```
// MTU is the largest datagram the path will carry, in bytes. Zero means
	// unlimited, which is what every link modelled here did before: any
	// datagram, however large, arrived.
	//
	// A datagram larger than this is dropped outright, which is what an IPv6
	// path does, and what an IPv4 path does for a packet marked don't-fragment.
	// It is counted separately from loss and from queue overflow, because it is
	// neither: it says something about size, and nothing about congestion.
	//
	// This exists because path-MTU discovery had never been tested against a
	// path that limits size. The search converged on whatever ceiling it was
	// given, every time, because nothing in the harness could refuse a large
	// packet -- so the half of the search that lowers the ceiling was never
	// exercised end to end.
```

## Line 122 — type Config

```
// OnMTUDrop, when set, is called for every datagram this link refuses for
	// being larger than MTU. It is the router that would have forwarded the
	// datagram sending an ICMP "fragmentation needed" message back to the
	// sender, and it carries what such a message carries: the front of the
	// offending datagram and the next-hop MTU.
	//
	// payload is the whole refused datagram; a real router quotes only its
	// front, so a test that wants to model that should truncate. src and dst
	// are the endpoint names this link runs between, so a caller can route
	// the report back to the socket that sent it. linkMTU is this link's MTU
	// expressed the way ICMP expresses it -- the largest IP datagram the hop
	// will carry -- which is the emulated link's MTU plus the IP and UDP
	// headers a real path would also have had to carry.
	//
	// It runs on the link's goroutine with no lock held, and must not block.
	//
	// Without this, an emulated path that refuses a large datagram is
	// indistinguishable from one that lost it, so the half of path-MTU
	// discovery that takes a router's word for the limit could not be tested
	// end to end at all.
```

## Line 144 — type Config

```
// OnOffered, if set, sees every datagram offered to this link, before
	// anything decides whether it is carried, dropped or delayed, with the
	// instant it was offered. The payload is a copy.
	//
	// It runs on the sender's goroutine with no lock held, and must not
	// block. It exists so a test can see what a sender puts on the wire and
	// when -- a retransmission burst is invisible in delivered-packet counts,
	// because the burst is exactly what a blackout drops.
```

## Line 154 — type Config

```
// FragmentOversized makes this link fragment a datagram larger than MTU
	// and forward it, instead of dropping it -- unless the sender set the
	// don't-fragment bit, in which case it is dropped as before.
	//
	// This is the difference between an IPv4 path and an IPv6 one, and it is
	// the only condition under which the don't-fragment bit changes anything.
	// With it false (the default) this link models IPv6: oversized is always
	// dropped, so a path-MTU search learns the truth whether or not it sets
	// the bit.
	//
	// With it true the link models IPv4, where an MTU probe sent without the
	// bit is fragmented, arrives, and is acknowledged -- telling the search
	// that a size the path cannot actually carry in one piece is fine. That
	// is the failure utp.DontFragmentWriter exists to prevent, and this is
	// where it can be observed.
```

## Line 171 — type Config

```
// Seed seeds this link's PRNG. Links in one Network derive distinct seeds
	// from Network's seed, so a single Seed makes a whole topology
	// reproducible.
```

## Line 178 — const DefaultQueueBytes, DefaultReorderDelay, inboxCapacity

```
// DefaultQueueBytes is a bottleneck queue of roughly 64 full packets.
```

## Line 180 — const DefaultQueueBytes, DefaultReorderDelay, inboxCapacity

```
// DefaultReorderDelay is the displacement applied to a reordered packet.
```

## Line 182 — const DefaultQueueBytes, DefaultReorderDelay, inboxCapacity

```
// inboxCapacity bounds an endpoint's delivered-but-unread backlog.
```

## Line 186 — type Peer

```
// Peer identifies an Endpoint. It satisfies utp.ConnectionPeer.
```

## Line 191 — func (*Peer).Hash

```
// Hash returns the peer's identity.
```

## Line 194 — func (*Peer).String

```
// String returns the peer's name.
```

## Line 197 — func (*Peer).Name

```
// Name returns the peer's name.
```

## Line 205 — type Endpoint

```
// Endpoint is one host on the emulated network. It satisfies utp.Conn.
```

## Line 215 — type Endpoint

```
// delivered but the inbox was full
```

## Line 218 — type Endpoint

```
// pathMTU, when non-zero, is the datagram size this endpoint reports
	// through utp.PathMTUProvider. Zero means it declines to answer,
	// which is the default and what every test written before the provider
	// existed relies on.
```

## Line 225 — func (*Endpoint).ReportPathMTU

```
// ReportPathMTU makes this endpoint answer utp.PathMTUProvider with
// datagramSize, as a host whose local interface is that narrow would.
//
// Opt-in, deliberately. An endpoint that always reported its link's MTU would
// hand every connection the right answer for free, which is realistic for a
// host behind a tunnel and useless for testing what happens without one --
// the ICMP tests in particular need the search to overshoot before a router
// can refuse anything.
//
// datagramSize is a uTP datagram size, the same unit Config.MTU is in: this
// emulated path has one hop, so the link limit and the local interface's
// limit are the same number.
```

## Line 243 — func (*Endpoint).PathMTU

```
// PathMTU implements utp.PathMTUProvider. It reports the same figure
// whatever the peer, because ReportPathMTU models a local interface rather
// than a route.
```

## Line 255 — func (*Endpoint).Addr

```
// Addr returns this endpoint's peer identity, for use as a uTP destination.
```

## Line 258 — func (*Endpoint).Name

```
// Name returns this endpoint's name.
```

## Line 261 — func (*Endpoint).ReadFrom

```
// ReadFrom blocks until a packet arrives, and satisfies utp.Conn.
```

## Line 278

```
// Endpoint does not implement utp.QueuedReader, so a socket on it treats
// each datagram as a read batch of its own and acknowledges it alone (unless
// the acknowledgement-rate hold defers it), which is what its measurements
// were taken with. A test that wants a receiver batching as a real socket
// does wraps it; see batchedReader in dont_fragment_test.go.
```

## Line 284 — func (*Endpoint).WriteTo

```
// WriteTo sends a packet towards dst, and satisfies utp.Conn.
//
// It never blocks: the packet is handed to the link, which decides whether it
// is dropped, queued or delivered. The return value reports bytes accepted for
// transmission, not bytes delivered -- exactly like a real UDP socket.
```

## Line 293 — func (*Endpoint).WriteToDontFragment

```
// WriteToDontFragment implements utp.DontFragmentWriter: the datagram is sent
// with fragmentation forbidden, so a link narrower than it drops it instead of
// fragmenting it.
//
// An emulated network can always honour this, which is the point of having it
// here: on a real socket the answer depends on the operating system, and a
// test that could only run where the socket option exists would not run in CI
// on every platform this library builds for.
//
// Note that it changes nothing unless Config.FragmentOversized is set. Without
// it a link drops everything over its MTU regardless, which models IPv6.
```

## Line 325 — func (*Endpoint).writeTo

```
// Copy: the caller owns b and may reuse it the moment we return.
```

## Line 332 — func (*Endpoint).Close

```
// Close shuts the endpoint down. It is idempotent.
```

## Line 338 — func (*Endpoint).deliver

```
// deliver hands a packet to the application side of this endpoint.
// It reports whether the packet was accepted; a full inbox means the reader
// is not keeping up, which is a real kernel-buffer overflow, counted as such.
```

## Line 362 — type Network

```
// Network is a set of endpoints joined by emulated links.
```

## Line 372 — func NewNetwork

```
// NewNetwork creates an empty network. seed makes the whole topology
// reproducible: each link derives its own PRNG seed from it.
```

## Line 382 — func (*Network).AddEndpoint

```
// AddEndpoint creates a named endpoint. Names must be unique.
```

## Line 399 — func (*Network).MustAddEndpoint

```
// MustAddEndpoint is AddEndpoint, panicking on error. For tests.
```

## Line 410 — func (*Network).Link

```
// Link returns the link carrying traffic from src to dst, or nil.
```

## Line 421 — func (*Network).Connect

```
// Connect joins a and b with an independent link in each direction, both
// using cfg. Use ConnectAsymmetric for different conditions per direction.
```

## Line 427 — func (*Network).ConnectAsymmetric

```
// ConnectAsymmetric joins a and b, applying aToB to traffic from a to b and
// bToA to traffic from b to a. Real paths are rarely symmetric.
```

## Line 434 — func (*Network).endpointFor

```
// endpointFor looks up an endpoint by name.
```

## Line 441 — func (*Network).ConnectShared

```
// ConnectShared wires several endpoint pairs through a single bottleneck:
// one queue, one rate, one loss draw, shared by every pair given.
//
// This is what makes flows actually compete. Connect and ConnectAsymmetric
// give each directed pair its own Link, so two flows between two different
// pairs of endpoints each get their own queue and their own full bandwidth --
// they run alongside each other without ever contending. A congestion
// controller cannot be judged against another controller on links like that,
// because neither one can crowd the other out.
//
// Each element of pairs is a {source, destination} pair, and every one of
// them is routed through the same Link, in the direction given. To share a
// bottleneck in both directions, pass both directions.
//
// The Link's stats and queue samples cover all the traffic through it, which
// is the point: the standing queue at a shared bottleneck is a property of
// the bottleneck, not of any one flow.
```

## Line 495 — func (*Network).addLink

```
// distinct stream per link
```

## Line 502 — func (*Network).Close

```
// Close shuts down every link and endpoint.
```

## Line 528 — func (*Network).SetConfig

```
// SetConfig replaces the configuration of the src->dst link while it is
// running, for tests that change conditions mid-transfer. In-flight packets
// keep the schedule they were already given.
```

## Line 548 — func newRand

```
// rngFor is exposed for tests that need to check seeding behaviour.
```
