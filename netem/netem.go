// Package netem provides an in-process emulated network for testing uTP.
//
// It exists because a congestion controller that passes unit tests tells you
// nothing. Too aggressive and it floods while claiming to be a background
// transport; too timid and it gets near-zero throughput. Against loopback both
// look like "it works". This package makes the difference measurable.
//
// An Endpoint satisfies the utp_go.Conn interface (ReadFrom/WriteTo/Close), so
// it drops straight into utp_go.WithSocket in place of a UDP socket. No root,
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
package netem

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// Endpoint must be usable anywhere a real UDP socket is.
var _ utp.Conn = (*Endpoint)(nil)

var (
	// ErrClosed is returned by ReadFrom and WriteTo after Close.
	ErrClosed = errors.New("netem: endpoint closed")
	// ErrNoRoute is returned when writing to an endpoint with no link.
	ErrNoRoute = errors.New("netem: no link to destination")
	// ErrBufferTooSmall is returned when a read buffer cannot hold the packet.
	ErrBufferTooSmall = errors.New("netem: buffer too small for packet")
)

// Config describes one direction of an emulated path. The zero value is a
// perfect link: no delay, no loss, unlimited bandwidth.
type Config struct {
	// Delay is the one-way propagation delay.
	Delay time.Duration

	// Jitter perturbs Delay by a uniform amount in [-Jitter, +Jitter].
	// Delivery order is preserved unless Jitter exceeds packet spacing.
	Jitter time.Duration

	// LossRate is the independent probability of dropping a packet, in [0,1].
	// This is loss on the medium; it is counted separately from queue
	// overflow, which is loss caused by congestion.
	LossRate float64

	// ReorderRate is the probability that a packet is displaced backwards in
	// the delivery order by ReorderDelay, in [0,1].
	ReorderRate float64

	// ReorderDelay is how much extra delay a reordered packet receives.
	// Defaults to DefaultReorderDelay.
	ReorderDelay time.Duration

	// BandwidthBps is the bottleneck rate in bits per second. Zero means
	// unlimited: packets are not serialized and never queue.
	//
	// Packets are serialized at this rate, so a packet's transmission
	// occupies len*8/BandwidthBps seconds during which no other packet on
	// this link can be sent. Flows sharing a link contend for it.
	BandwidthBps uint64

	// QueueBytes is the bottleneck queue capacity. A packet arriving when the
	// queue is full is tail-dropped. Defaults to DefaultQueueBytes.
	//
	// This is what bounds standing queueing delay, and therefore what a
	// delay-based controller is supposed to keep clear of.
	QueueBytes int

	// Seed seeds this link's PRNG. Links in one Network derive distinct seeds
	// from Network's seed, so a single Seed makes a whole topology
	// reproducible.
	Seed int64
}

const (
	// DefaultQueueBytes is a bottleneck queue of roughly 64 full packets.
	DefaultQueueBytes = 64 * 1024
	// DefaultReorderDelay is the displacement applied to a reordered packet.
	DefaultReorderDelay = 20 * time.Millisecond
	// inboxCapacity bounds an endpoint's delivered-but-unread backlog.
	inboxCapacity = 4096
)

// Peer identifies an Endpoint. It satisfies utp_go.ConnectionPeer.
type Peer struct {
	name string
}

// Hash returns the peer's identity.
func (p *Peer) Hash() string { return p.name }

// String returns the peer's name.
func (p *Peer) String() string { return p.name }

// Name returns the peer's name.
func (p *Peer) Name() string { return p.name }

type inboundPacket struct {
	payload []byte
	from    *Peer
}

// Endpoint is one host on the emulated network. It satisfies utp_go.Conn.
type Endpoint struct {
	net   *Network
	peer  *Peer
	inbox chan inboundPacket

	closeOnce sync.Once
	closed    chan struct{}

	mu       sync.Mutex
	dropped  uint64 // delivered but the inbox was full
	received uint64
	rxBytes  uint64
}

// Addr returns this endpoint's peer identity, for use as a uTP destination.
func (e *Endpoint) Addr() *Peer { return e.peer }

// Name returns this endpoint's name.
func (e *Endpoint) Name() string { return e.peer.name }

// ReadFrom blocks until a packet arrives, and satisfies utp_go.Conn.
func (e *Endpoint) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	select {
	case pkt, ok := <-e.inbox:
		if !ok {
			return 0, nil, ErrClosed
		}
		if len(b) < len(pkt.payload) {
			return 0, nil, ErrBufferTooSmall
		}
		n := copy(b, pkt.payload)
		return n, pkt.from, nil
	case <-e.closed:
		return 0, nil, ErrClosed
	}
}

// WriteTo sends a packet towards dst, and satisfies utp_go.Conn.
//
// It never blocks: the packet is handed to the link, which decides whether it
// is dropped, queued or delivered. The return value reports bytes accepted for
// transmission, not bytes delivered -- exactly like a real UDP socket.
func (e *Endpoint) WriteTo(b []byte, dst utp.ConnectionPeer) (int, error) {
	select {
	case <-e.closed:
		return 0, ErrClosed
	default:
	}
	if dst == nil {
		return 0, ErrNoRoute
	}
	link := e.net.linkFor(e.peer.name, dst.Hash())
	if link == nil {
		return 0, fmt.Errorf("%w: %s -> %s", ErrNoRoute, e.peer.name, dst.Hash())
	}
	dstEndpoint := e.net.endpointFor(dst.Hash())
	if dstEndpoint == nil {
		return 0, fmt.Errorf("%w: %s -> %s", ErrNoRoute, e.peer.name, dst.Hash())
	}
	// Copy: the caller owns b and may reuse it the moment we return.
	payload := make([]byte, len(b))
	copy(payload, b)
	link.enqueue(payload, e, dstEndpoint)
	return len(b), nil
}

// Close shuts the endpoint down. It is idempotent.
func (e *Endpoint) Close() error {
	e.closeOnce.Do(func() { close(e.closed) })
	return nil
}

// deliver hands a packet to the application side of this endpoint.
// It reports whether the packet was accepted; a full inbox means the reader
// is not keeping up, which is a real kernel-buffer overflow, counted as such.
func (e *Endpoint) deliver(pkt inboundPacket) bool {
	select {
	case <-e.closed:
		return false
	default:
	}
	select {
	case e.inbox <- pkt:
		e.mu.Lock()
		e.received++
		e.rxBytes += uint64(len(pkt.payload))
		e.mu.Unlock()
		return true
	default:
		e.mu.Lock()
		e.dropped++
		e.mu.Unlock()
		return false
	}
}

// Network is a set of endpoints joined by emulated links.
type Network struct {
	mu        sync.RWMutex
	endpoints map[string]*Endpoint
	links     map[string]*Link
	seed      int64
	linkSeq   int64
	closed    bool
}

// NewNetwork creates an empty network. seed makes the whole topology
// reproducible: each link derives its own PRNG seed from it.
func NewNetwork(seed int64) *Network {
	return &Network{
		endpoints: make(map[string]*Endpoint),
		links:     make(map[string]*Link),
		seed:      seed,
	}
}

// AddEndpoint creates a named endpoint. Names must be unique.
func (n *Network) AddEndpoint(name string) (*Endpoint, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, exists := n.endpoints[name]; exists {
		return nil, fmt.Errorf("netem: duplicate endpoint %q", name)
	}
	ep := &Endpoint{
		net:    n,
		peer:   &Peer{name: name},
		inbox:  make(chan inboundPacket, inboxCapacity),
		closed: make(chan struct{}),
	}
	n.endpoints[name] = ep
	return ep, nil
}

// MustAddEndpoint is AddEndpoint, panicking on error. For tests.
func (n *Network) MustAddEndpoint(name string) *Endpoint {
	ep, err := n.AddEndpoint(name)
	if err != nil {
		panic(err)
	}
	return ep
}

func linkKey(src, dst string) string { return src + "\x00" + dst }

// Link returns the link carrying traffic from src to dst, or nil.
func (n *Network) Link(src, dst string) *Link {
	return n.linkFor(src, dst)
}

func (n *Network) linkFor(src, dst string) *Link {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.links[linkKey(src, dst)]
}

// Connect joins a and b with an independent link in each direction, both
// using cfg. Use ConnectAsymmetric for different conditions per direction.
func (n *Network) Connect(a, b *Endpoint, cfg Config) {
	n.ConnectAsymmetric(a, b, cfg, cfg)
}

// ConnectAsymmetric joins a and b, applying aToB to traffic from a to b and
// bToA to traffic from b to a. Real paths are rarely symmetric.
func (n *Network) ConnectAsymmetric(a, b *Endpoint, aToB, bToA Config) {
	n.addLink(a, b, aToB)
	n.addLink(b, a, bToA)
}

// endpointFor looks up an endpoint by name.
func (n *Network) endpointFor(name string) *Endpoint {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.endpoints[name]
}

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
func (n *Network) ConnectShared(cfg Config, pairs ...[2]*Endpoint) {
	if len(pairs) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if cfg.QueueBytes <= 0 {
		cfg.QueueBytes = DefaultQueueBytes
	}
	if cfg.ReorderDelay <= 0 {
		cfg.ReorderDelay = DefaultReorderDelay
	}
	seed := cfg.Seed
	if seed == 0 {
		n.linkSeq++
		seed = n.seed + n.linkSeq*7919
	}
	l := newLink(pairs[0][0], pairs[0][1], cfg, seed)
	for _, pair := range pairs {
		n.links[linkKey(pair[0].peer.name, pair[1].peer.name)] = l
	}

	go l.run()
}

func (n *Network) addLink(src, dst *Endpoint, cfg Config) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if cfg.QueueBytes <= 0 {
		cfg.QueueBytes = DefaultQueueBytes
	}
	if cfg.ReorderDelay <= 0 {
		cfg.ReorderDelay = DefaultReorderDelay
	}
	seed := cfg.Seed
	if seed == 0 {
		n.linkSeq++
		seed = n.seed + n.linkSeq*7919 // distinct stream per link
	}
	l := newLink(src, dst, cfg, seed)
	n.links[linkKey(src.peer.name, dst.peer.name)] = l
	go l.run()
}

// Close shuts down every link and endpoint.
func (n *Network) Close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	links := make([]*Link, 0, len(n.links))
	for _, l := range n.links {
		links = append(links, l)
	}
	eps := make([]*Endpoint, 0, len(n.endpoints))
	for _, e := range n.endpoints {
		eps = append(eps, e)
	}
	n.mu.Unlock()

	for _, l := range links {
		l.stop()
	}
	for _, e := range eps {
		_ = e.Close()
	}
}

// SetConfig replaces the configuration of the src->dst link while it is
// running, for tests that change conditions mid-transfer. In-flight packets
// keep the schedule they were already given.
func (n *Network) SetConfig(src, dst string, cfg Config) error {
	l := n.linkFor(src, dst)
	if l == nil {
		return fmt.Errorf("%w: %s -> %s", ErrNoRoute, src, dst)
	}
	if cfg.QueueBytes <= 0 {
		cfg.QueueBytes = DefaultQueueBytes
	}
	if cfg.ReorderDelay <= 0 {
		cfg.ReorderDelay = DefaultReorderDelay
	}
	l.mu.Lock()
	l.cfg = cfg
	l.mu.Unlock()
	return nil
}

// rngFor is exposed for tests that need to check seeding behaviour.
func newRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }
