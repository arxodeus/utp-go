//go:build cgo

package libutp_test

import (
	"container/heap"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

// A userspace impairment relay between two real UDP sockets.
//
// The adverse-conditions tests in netem damage packets inside an emulated
// network, where libutp is driven by a Go loop on a clock the loop sets. Here
// both implementations run as they would in production -- libutp on its own
// kernel socket, its own thread and the real clock, this library on a real
// utpnet socket -- and only the path between them is artificial.
//
// It is artificial in userspace because it has to be: this host's kernel is
// built without netem (CONFIG_NET_SCH_NETEM is not set) and has no tc, so
// loopback cannot be told to lose or reorder anything. Every datagram still
// leaves one kernel socket and arrives at another; the relay only decides
// which of them arrive, and when.
//
// Topology: libutp <-> relay.sideA ... relay.sideB <-> ours. Whatever arrives
// on sideA is forwarded from sideB to our socket, and whatever arrives on
// sideB is forwarded from sideA to libutp's, so each implementation sees the
// relay as its peer.

// pathConfig is one direction's impairment.
type pathConfig struct {
	Delay        time.Duration // one-way propagation
	Jitter       time.Duration // uniform in [-Jitter, +Jitter] around Delay, as in netem
	BandwidthBps int64         // bottleneck rate; zero means unlimited
	QueueBytes   int           // bottleneck queue; a datagram that does not fit is dropped
	LossRate     float64       // independent random loss
	ReorderRate  float64       // fraction held back by ReorderHold, so later ones overtake
	ReorderHold  time.Duration
}

// pathStats counts what one direction did.
type pathStats struct {
	Offered, Delivered, DroppedByLoss, DroppedByQueue, Reordered uint64
}

type relayItem struct {
	at      time.Time
	seq     uint64
	payload []byte
}

type relayHeap []relayItem

func (h relayHeap) Len() int { return len(h) }
func (h relayHeap) Less(i, j int) bool {
	if !h[i].at.Equal(h[j].at) {
		return h[i].at.Before(h[j].at)
	}
	return h[i].seq < h[j].seq
}
func (h relayHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *relayHeap) Push(x any)   { *h = append(*h, x.(relayItem)) }
func (h *relayHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// relayPath is one direction: datagrams read from in are written from out to
// dst after the impairment decides their fate.
type relayPath struct {
	cfg pathConfig
	out *net.UDPConn
	dst *net.UDPAddr

	mu       sync.Mutex
	rng      *rand.Rand
	pending  relayHeap
	seq      uint64
	nextFree time.Time // when the bottleneck finishes serializing its queue
	queued   int       // bytes waiting in the bottleneck queue
	stats    pathStats
	wake     chan struct{}
}

func (p *relayPath) offer(b []byte, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.Offered++
	if p.cfg.LossRate > 0 && p.rng.Float64() < p.cfg.LossRate {
		p.stats.DroppedByLoss++
		return
	}
	depart := now
	if p.cfg.BandwidthBps > 0 {
		// Bytes still waiting to be serialized are the queue.
		if p.nextFree.After(now) {
			p.queued = int(float64(p.nextFree.Sub(now)) / float64(time.Second) * float64(p.cfg.BandwidthBps) / 8)
		} else {
			p.queued, p.nextFree = 0, now
		}
		if p.cfg.QueueBytes > 0 && p.queued+len(b) > p.cfg.QueueBytes {
			p.stats.DroppedByQueue++
			return
		}
		p.nextFree = p.nextFree.Add(time.Duration(float64(len(b)*8) / float64(p.cfg.BandwidthBps) * float64(time.Second)))
		depart = p.nextFree
	}
	delay := p.cfg.Delay
	if p.cfg.Jitter > 0 {
		delay += time.Duration(p.rng.Int63n(int64(2*p.cfg.Jitter)+1)) - p.cfg.Jitter
		if delay < 0 {
			delay = 0
		}
	}
	at := depart.Add(delay)
	if p.cfg.ReorderRate > 0 && p.rng.Float64() < p.cfg.ReorderRate {
		at = at.Add(p.cfg.ReorderHold)
		p.stats.Reordered++
	}
	p.seq++
	heap.Push(&p.pending, relayItem{at: at, seq: p.seq, payload: append([]byte(nil), b...)})
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// deliver writes out each datagram when it is due, until done closes.
func (p *relayPath) deliver(done <-chan struct{}) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		p.mu.Lock()
		now := time.Now()
		var due [][]byte
		for p.pending.Len() > 0 && !p.pending[0].at.After(now) {
			due = append(due, heap.Pop(&p.pending).(relayItem).payload)
		}
		wait := time.Hour
		if p.pending.Len() > 0 {
			wait = p.pending[0].at.Sub(now)
		}
		p.stats.Delivered += uint64(len(due))
		p.mu.Unlock()

		for _, b := range due {
			_, _ = p.out.WriteToUDP(b, p.dst)
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-p.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-done:
			return
		}
	}
}

func (p *relayPath) Stats() pathStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// relay joins libutp's socket to ours through two impaired paths.
type relay struct {
	sideA, sideB *net.UDPConn // sideA faces libutp, sideB faces us
	toGo, toLib  *relayPath
	done         chan struct{}
	wg           sync.WaitGroup
}

// newRelay starts a relay between libutp at libPort and our socket at goPort.
// toGo impairs what libutp sends; toLib what we send.
func newRelay(t *testing.T, libPort, goPort uint16, toGo, toLib pathConfig, seed int64) *relay {
	t.Helper()
	listen := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadBuffer(4 << 20)
		_ = c.SetWriteBuffer(4 << 20)
		return c
	}
	r := &relay{sideA: listen(), sideB: listen(), done: make(chan struct{})}
	r.toGo = &relayPath{cfg: toGo, out: r.sideB, dst: loopback(goPort),
		rng: rand.New(rand.NewSource(seed)), wake: make(chan struct{}, 1)}
	r.toLib = &relayPath{cfg: toLib, out: r.sideA, dst: loopback(libPort),
		rng: rand.New(rand.NewSource(seed + 1)), wake: make(chan struct{}, 1)}

	pump := func(in *net.UDPConn, p *relayPath) {
		defer r.wg.Done()
		buf := make([]byte, 65536)
		for {
			n, _, err := in.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p.offer(buf[:n], time.Now())
		}
	}
	r.wg.Add(4)
	go pump(r.sideA, r.toGo)
	go pump(r.sideB, r.toLib)
	go func() { defer r.wg.Done(); r.toGo.deliver(r.done) }()
	go func() { defer r.wg.Done(); r.toLib.deliver(r.done) }()
	return r
}

// LibutpFacingPort is where libutp should send: the relay's side facing it.
func (r *relay) LibutpFacingPort() uint16 { return uint16(r.sideA.LocalAddr().(*net.UDPAddr).Port) }

// GoFacingAddr is where our socket should send.
func (r *relay) GoFacingAddr() *net.UDPAddr { return r.sideB.LocalAddr().(*net.UDPAddr) }

func (r *relay) Close() {
	close(r.done)
	_ = r.sideA.Close()
	_ = r.sideB.Close()
	r.wg.Wait()
}
