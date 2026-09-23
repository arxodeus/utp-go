package netem

import (
	"container/heap"
	"math/rand"
	"sync"
	"time"
)

// DropCause records why the link discarded a packet.
type DropCause int

const (
	// DropLoss is loss on the medium, from Config.LossRate.
	DropLoss DropCause = iota
	// DropQueueOverflow is congestion loss: the bottleneck queue was full.
	DropQueueOverflow
	// DropReceiverOverflow means the packet arrived but the destination's
	// inbox was full -- the reader is not keeping up. This models a kernel
	// receive-buffer overflow, not a network event.
	DropReceiverOverflow
)

// String names the drop cause.
func (d DropCause) String() string {
	switch d {
	case DropLoss:
		return "loss"
	case DropQueueOverflow:
		return "queue-overflow"
	case DropReceiverOverflow:
		return "receiver-overflow"
	default:
		return "unknown"
	}
}

// scheduled is a packet with its delivery time.
type scheduled struct {
	arriveAt time.Time
	payload  []byte
	// seq orders packets scheduled for the same instant, so the heap is a
	// stable FIFO rather than depending on heap internals.
	seq uint64
	// queueDelay is how long this packet waited for the bottleneck.
	queueDelay time.Duration
	// sizeBytes is retained so the queue accounting can be released on
	// departure.
	sizeBytes int
	// src and dst are this packet's endpoints. They are per packet rather
	// than per link because one Link can be shared by several endpoint
	// pairs -- that is what makes a shared bottleneck, where two flows
	// contend for one queue and one rate. See Network.ConnectShared.
	src, dst *Endpoint
}

type deliveryHeap []*scheduled

func (h deliveryHeap) Len() int { return len(h) }
func (h deliveryHeap) Less(i, j int) bool {
	if h[i].arriveAt.Equal(h[j].arriveAt) {
		return h[i].seq < h[j].seq
	}
	return h[i].arriveAt.Before(h[j].arriveAt)
}
func (h deliveryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *deliveryHeap) Push(x any)   { *h = append(*h, x.(*scheduled)) }
func (h *deliveryHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

// Link is one direction of an emulated path. Traffic from several flows
// between the same pair of endpoints shares one Link, and therefore contends
// for its bandwidth and its queue.
type Link struct {
	src, dst *Endpoint

	mu  sync.Mutex
	cfg Config
	rng *rand.Rand

	// nextFree is when the bottleneck finishes transmitting whatever is
	// currently being serialized. Serialization is what creates a queue.
	nextFree time.Time
	// queuedBytes is the backlog currently occupying the bottleneck queue.
	queuedBytes int

	pending deliveryHeap
	seq     uint64

	stats Stats
	// samples records queueing delay over time for post-run analysis.
	samples []QueueSample

	wake     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newLink(src, dst *Endpoint, cfg Config, seed int64) *Link {
	return &Link{
		src:  src,
		dst:  dst,
		cfg:  cfg,
		rng:  newRand(seed),
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

// icmpHeaderOverhead is what a real IPv4 path spends on headers below the UDP
// payload: 20 bytes of IP and 8 of UDP. This emulator's MTU bounds the uTP
// datagram, so a report quoting a *link* MTU has to add them back.
const icmpHeaderOverhead = 28

// enqueue applies the link model to a packet and schedules its delivery.
// It never blocks.
func (l *Link) enqueue(payload []byte, src, dst *Endpoint) {
	l.enqueueWithFlags(payload, src, dst, false)
}

// enqueueWithFlags is enqueue, plus whether the sender forbade fragmentation.
func (l *Link) enqueueWithFlags(payload []byte, src, dst *Endpoint, dontFragment bool) {
	now := time.Now()
	size := len(payload)

	l.mu.Lock()
	onOffered := l.cfg.OnOffered
	l.mu.Unlock()
	if onOffered != nil {
		onOffered(append([]byte(nil), payload...), now)
	}

	l.mu.Lock()
	cfg := l.cfg
	l.stats.PacketsOffered++
	l.stats.BytesOffered += uint64(size)

	// 0. Size. A datagram larger than the path will carry is dropped before
	//    anything else, because it never gets onto the wire at all and its
	//    loss carries no information about congestion. This is what makes
	//    path-MTU discovery testable: the search's ceiling only comes down
	//    when a probe is refused for being too big.
	if cfg.MTU > 0 && size > cfg.MTU {
		// An IPv4 router with something too big for the next hop has two
		// choices, and which one it takes is exactly what the don't-fragment
		// bit decides. Without FragmentOversized this link only ever makes the
		// second choice, which models IPv6 -- correct, and the reason the
		// don't-fragment bit made no difference here until now.
		if cfg.FragmentOversized && !dontFragment {
			// Fragmented and forwarded. It arrives, so the sender learns
			// nothing about the path being narrow -- which is the trap: an
			// MTU probe sent without the bit is acknowledged, the search's
			// floor rises, and it settles on a size that only works because
			// every packet at it is being fragmented.
			//
			// Counted, not dropped: execution falls through to the rest of
			// the pipeline, so a fragmented datagram is still subject to
			// loss, queueing and delay like any other. Nothing here models
			// the *cost* of fragmentation -- a real path pays in headers and
			// in a whole datagram lost when any one fragment is -- because
			// what is being tested is what the search concludes, and it
			// concludes it from the packet arriving at all.
			l.stats.PacketsFragmented++
			goto accepted
		}
		l.stats.PacketsDropped++
		l.stats.DroppedByMTU++
		l.mu.Unlock()
		if cfg.OnMTUDrop != nil {
			// The router tells the sender why. Called outside the lock: the
			// callback reaches back into a uTP socket, which must not be able
			// to deadlock against this link.
			//
			// The datagram is copied because the caller's buffer is reused,
			// and the quoted MTU is the link MTU an ICMP message would carry:
			// this emulator's MTU limits the uTP datagram, so the headers a
			// real path also carries are added back.
			quoted := make([]byte, size)
			copy(quoted, payload)
			cfg.OnMTUDrop(quoted, src.Name(), dst.Name(), cfg.MTU+icmpHeaderOverhead)
		}
		return
	}
accepted:

	// 1. Loss on the medium. Drawn before queueing so a lost packet does not
	//    occupy the bottleneck -- it never made it onto the wire.
	if cfg.LossRate > 0 && l.rng.Float64() < cfg.LossRate {
		l.stats.PacketsDropped++
		l.stats.DroppedByLoss++
		l.mu.Unlock()
		return
	}

	// 2. Bottleneck serialization and queueing.
	var departAt time.Time
	var serviceTime time.Duration
	var queueDelay time.Duration
	if cfg.BandwidthBps > 0 {
		serviceTime = time.Duration(float64(size) * 8 * float64(time.Second) / float64(cfg.BandwidthBps))

		// Release backlog that has drained since the last packet.
		if l.nextFree.Before(now) {
			l.nextFree = now
			l.queuedBytes = 0
		} else {
			// Bytes still ahead of us in the queue, approximated from the
			// remaining busy time at the link rate.
			busy := l.nextFree.Sub(now)
			l.queuedBytes = int(float64(busy) / float64(time.Second) * float64(cfg.BandwidthBps) / 8)
		}

		if l.queuedBytes+size > cfg.QueueBytes {
			// Tail drop: this is congestion loss, the signal a congestion
			// controller is meant to respond to.
			l.stats.PacketsDropped++
			l.stats.DroppedByQueue++
			l.mu.Unlock()
			return
		}

		departAt = l.nextFree
		queueDelay = departAt.Sub(now)
		l.nextFree = departAt.Add(serviceTime)
		l.queuedBytes += size
	} else {
		departAt = now
	}

	// 3. Propagation delay, plus jitter.
	delay := cfg.Delay
	if cfg.Jitter > 0 {
		// Uniform in [-Jitter, +Jitter].
		delay += time.Duration(l.rng.Int63n(int64(2*cfg.Jitter)+1)) - cfg.Jitter
		if delay < 0 {
			delay = 0
		}
	}

	// 4. Reordering: displace this packet behind those that follow it.
	if cfg.ReorderRate > 0 && l.rng.Float64() < cfg.ReorderRate {
		delay += cfg.ReorderDelay
		l.stats.PacketsReordered++
	}

	arriveAt := departAt.Add(serviceTime).Add(delay)

	l.seq++
	if src == nil {
		src = l.src
	}
	if dst == nil {
		dst = l.dst
	}
	item := &scheduled{
		arriveAt:   arriveAt,
		payload:    payload,
		seq:        l.seq,
		queueDelay: queueDelay,
		sizeBytes:  size,
		src:        src,
		dst:        dst,
	}
	heap.Push(&l.pending, item)

	l.stats.QueueDelaySum += queueDelay
	l.stats.QueueDelayCount++
	if queueDelay > l.stats.QueueDelayMax {
		l.stats.QueueDelayMax = queueDelay
	}
	l.samples = append(l.samples, QueueSample{At: now, Delay: queueDelay, BacklogBytes: l.queuedBytes})
	l.mu.Unlock()

	// Nudge the delivery goroutine: the new packet may be due before whatever
	// it is currently waiting for.
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// run delivers scheduled packets at their arrival times.
func (l *Link) run() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		l.mu.Lock()
		now := time.Now()
		// Deliver everything already due, in one pass. Batching matters: at
		// high rates packets are spaced far more finely than the runtime's
		// timer granularity, so waking per packet would not keep up.
		var due []*scheduled
		for l.pending.Len() > 0 && !l.pending[0].arriveAt.After(now) {
			due = append(due, heap.Pop(&l.pending).(*scheduled))
		}
		var wait time.Duration
		if l.pending.Len() > 0 {
			wait = l.pending[0].arriveAt.Sub(now)
		} else {
			wait = time.Hour
		}
		l.mu.Unlock()

		for _, item := range due {
			ok := item.dst.deliver(inboundPacket{payload: item.payload, from: item.src.peer})
			l.mu.Lock()
			if ok {
				l.stats.PacketsDelivered++
				l.stats.BytesDelivered += uint64(item.sizeBytes)
			} else {
				l.stats.PacketsDropped++
				l.stats.DroppedByReceiver++
			}
			l.mu.Unlock()
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)

		select {
		case <-timer.C:
		case <-l.wake:
		case <-l.done:
			return
		}
	}
}

func (l *Link) stop() {
	l.stopOnce.Do(func() { close(l.done) })
}
