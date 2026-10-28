package netem

import (
	"math"
	"sort"
	"sync"
	"testing"
	"time"
)

// Properties beyond the three headline gates. A congestion test is only worth
// running if reordering, jitter and queue bounding behave as configured, and
// only worth keeping if it reproduces.

func TestReorderingHappensAtConfiguredRate(t *testing.T) {
	const rate = 0.10
	n := NewNetwork(11)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        5 * time.Millisecond,
		ReorderRate:  rate,
		ReorderDelay: 30 * time.Millisecond,
	})

	d := startDrain(b)
	const offered = 2000
	for i := 0; i < offered; i++ {
		if _, err := a.WriteTo(makePayload(uint32(i), 64), b.Addr()); err != nil {
			t.Fatal(err)
		}
		if i%200 == 199 {
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond)
	st := n.Link("a", "b").Stats()
	seqs := d.seqs()
	d.stop()

	observedRate := float64(st.PacketsReordered) / float64(st.PacketsOffered)

	// Count inversions: a delivered sequence number lower than one already
	// seen means a packet actually arrived late, not merely that it was
	// flagged.
	inversions := 0
	var high uint32
	for i, s := range seqs {
		if i > 0 && s < high {
			inversions++
		}
		if s > high {
			high = s
		}
	}
	t.Logf("configured reorder rate %.2f, flagged %.4f (%d of %d); %d inversions in %d delivered",
		rate, observedRate, st.PacketsReordered, st.PacketsOffered, inversions, len(seqs))

	sigma := math.Sqrt(rate * (1 - rate) / float64(offered))
	if math.Abs(observedRate-rate) > 3*sigma {
		t.Errorf("reorder rate %.4f differs from configured %.2f by more than 3 sigma (%.4f)", observedRate, rate, 3*sigma)
	}
	if inversions == 0 {
		t.Error("no packets actually arrived out of order; reordering is flagged but not applied")
	}
}

func TestNoReorderingWhenDisabled(t *testing.T) {
	n := NewNetwork(12)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: 2 * time.Millisecond})

	d := startDrain(b)
	const offered = 1500
	for i := 0; i < offered; i++ {
		if _, err := a.WriteTo(makePayload(uint32(i), 64), b.Addr()); err != nil {
			t.Fatal(err)
		}
		if i%200 == 199 {
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond)
	seqs := d.seqs()
	d.stop()

	for i := 1; i < len(seqs); i++ {
		if seqs[i] < seqs[i-1] {
			t.Fatalf("packets arrived out of order without reordering configured: %d after %d (index %d)",
				seqs[i], seqs[i-1], i)
		}
	}
	t.Logf("%d packets all delivered in order", len(seqs))
}

func TestJitterVariesDelayWithinBounds(t *testing.T) {
	const base = 40 * time.Millisecond
	const jitter = 10 * time.Millisecond
	n := NewNetwork(13)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: base, Jitter: jitter})

	buf := make([]byte, 65535)
	const samples = 40
	observed := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		sent := time.Now()
		if _, err := a.WriteTo(makePayload(uint32(i), 64), b.Addr()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
		observed = append(observed, time.Since(sent))
	}

	sorted := append([]time.Duration(nil), observed...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	min, max := sorted[0], sorted[len(sorted)-1]
	var sum time.Duration
	for _, d := range observed {
		sum += d
	}
	mean := sum / time.Duration(len(observed))
	p95 := sorted[int(0.95*float64(len(sorted)-1))]
	t.Logf("base %v +/- %v: observed min %v mean %v p95 %v max %v",
		base, jitter, min.Round(time.Microsecond), mean.Round(time.Microsecond),
		p95.Round(time.Microsecond), max.Round(time.Microsecond))

	// Assertions are on the distribution, not on the extreme. A single OS
	// scheduling hiccup moves the max by tens of milliseconds, and a harness
	// test that flakes gets deleted -- after which nothing is tested.
	if max-min < jitter {
		t.Errorf("observed spread %v is smaller than the configured jitter %v -- jitter is not being applied", max-min, jitter)
	}
	// The emulator can only add delay, never remove it, so nothing may arrive
	// earlier than base-jitter. This is an invariant, not a statistic.
	if min < base-jitter {
		t.Errorf("observed min %v is below base-jitter (%v); the emulator delivered early", min, base-jitter)
	}
	// The mean of a uniform distribution sits at its centre.
	if d := mean - base; d < -3*time.Millisecond || d > 5*time.Millisecond {
		t.Errorf("observed mean %v is not centred on the base delay %v (off by %v)", mean, base, d)
	}
	if p95 > base+jitter+5*time.Millisecond {
		t.Errorf("observed p95 %v exceeds base+jitter (%v) by more than the 5ms scheduler allowance", p95, base+jitter)
	}
	// A loose ceiling to catch gross errors without flaking on scheduler noise.
	if max > base+jitter+50*time.Millisecond {
		t.Errorf("observed max %v is far beyond base+jitter (%v)", max, base+jitter)
	}
}

// The bottleneck queue is what bounds standing delay. A delay-based
// controller is judged on how far below this ceiling it keeps the queue, so
// the ceiling itself has to be right.
func TestQueueDelayBoundedByQueueSize(t *testing.T) {
	const bw = 4_000_000
	const queueBytes = 16 * 1024
	n := NewNetwork(14)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{BandwidthBps: bw, QueueBytes: queueBytes})

	d := startDrain(b)
	offerAtRate(t, a, b, bw, 3.0, 1500*time.Millisecond)
	st := n.Link("a", "b").Stats()
	p50 := n.Link("a", "b").QueueDelayPercentile(0.5)
	p95 := n.Link("a", "b").QueueDelayPercentile(0.95)
	d.stop()

	// A full queue holds queueBytes, which drains at bw.
	ceiling := time.Duration(float64(queueBytes) * 8 * float64(time.Second) / float64(bw))
	t.Logf("queue %d bytes at %.1f Mbps -> ceiling %v; observed mean %v p50 %v p95 %v max %v",
		queueBytes, float64(bw)/1e6, ceiling.Round(time.Microsecond),
		st.MeanQueueDelay().Round(time.Microsecond), p50.Round(time.Microsecond),
		p95.Round(time.Microsecond), st.QueueDelayMax.Round(time.Microsecond))

	if st.QueueDelayMax > ceiling+2*time.Millisecond {
		t.Errorf("observed max queue delay %v exceeds the ceiling %v implied by the queue size", st.QueueDelayMax, ceiling)
	}
	// Persistent overload should drive the queue close to full.
	if st.QueueDelayMax < ceiling/2 {
		t.Errorf("sustained 3x overload only reached %v of a %v ceiling; the queue is not filling", st.QueueDelayMax, ceiling)
	}
	if st.DroppedByQueue == 0 {
		t.Error("sustained overload produced no tail drops")
	}
}

// Determinism is the whole reason for seeding. A congestion test that flakes
// gets deleted, and then nothing is tested.
func TestSameSeedReproducesSameDrops(t *testing.T) {
	run := func(seed int64) []uint32 {
		n := NewNetwork(seed)
		defer n.Close()
		a := n.MustAddEndpoint("a")
		b := n.MustAddEndpoint("b")
		n.Connect(a, b, Config{LossRate: 0.2, Delay: time.Millisecond})

		d := startDrain(b)
		for i := 0; i < 3000; i++ {
			if _, err := a.WriteTo(makePayload(uint32(i), 64), b.Addr()); err != nil {
				t.Fatal(err)
			}
			if i%300 == 299 {
				time.Sleep(time.Millisecond)
			}
		}
		time.Sleep(300 * time.Millisecond)
		seqs := d.seqs()
		d.stop()
		return seqs
	}

	first := run(4242)
	second := run(4242)

	if len(first) != len(second) {
		t.Fatalf("same seed delivered different counts: %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("same seed diverged at index %d: %d vs %d", i, first[i], second[i])
		}
	}
	t.Logf("seed 4242 reproduced an identical %d-packet delivery sequence", len(first))

	third := run(9999)
	if len(third) == len(first) {
		same := true
		for i := range first {
			if first[i] != third[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("different seeds produced an identical delivery sequence; the seed is not being used")
		}
	}
}

// Two flows over one link must contend for the same bottleneck. This is the
// mechanism the M5 fairness gates depend on; if flows did not actually share
// a queue, those gates would measure nothing.
func TestTwoFlowsShareBottleneck(t *testing.T) {
	const bw = 8_000_000
	n := NewNetwork(15)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{BandwidthBps: bw, QueueBytes: 32 * 1024})

	// Tag each flow in the payload so delivery can be attributed.
	var mu sync.Mutex
	delivered := map[byte]int{}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		for {
			nb, _, err := b.ReadFrom(buf)
			if err != nil {
				return
			}
			if nb > 4 {
				mu.Lock()
				delivered[buf[4]]++
				mu.Unlock()
			}
		}
	}()

	// One feeder, alternating between the two flows.
	//
	// This is a property of the link, not of any congestion control: given an
	// evenly interleaved arrival pattern, a FIFO queue should deliver an even
	// split. Two independent goroutines cannot establish that, because what
	// each one manages to *offer* is itself decided by the Go scheduler, and
	// Jain computed over delivered counts then conflates the queue's fairness
	// with the scheduler's.
	//
	// That is not a hypothetical. With two goroutines the test failed at 0.885
	// inside a full-package run while passing alone; reducing each burst from
	// 50 packets to 4 -- the offered load had been ten times the link rate,
	// not the "~75%" its comment claimed -- made it far better but not
	// reliable, and it still produced 0.826 once in eight runs. One feeder
	// removes the variance rather than narrowing it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		tags := []byte{'x', 'y'}
		var seq uint32
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			// Eight packets a tick, alternating, is about 160% of the link
			// between them: enough to keep the queue busy and contended
			// without the offered load itself becoming the variable.
			for i := 0; i < 8; i++ {
				seq++
				p := makePayload(seq, testPayload)
				p[4] = tags[i%2]
				if _, err := a.WriteTo(p, b.Addr()); err != nil {
					return
				}
			}
		}
	}()

	time.Sleep(500 * time.Millisecond) // warm up
	n.Link("a", "b").ResetStats()
	mu.Lock()
	delivered = map[byte]int{}
	mu.Unlock()

	const window = 2 * time.Second
	time.Sleep(window)

	st := n.Link("a", "b").Stats()
	mu.Lock()
	x, y := delivered['x'], delivered['y']
	mu.Unlock()
	close(stop)
	_ = b.Close()
	wg.Wait()

	total := Throughput{Bytes: st.BytesDelivered, Duration: window}
	fairness := FairnessIndex([]float64{float64(x), float64(y)})
	t.Logf("link %.1f Mbps carried %s; flow x %d pkts, flow y %d pkts, Jain fairness %.3f",
		float64(bw)/1e6, total, x, y, fairness)
	t.Logf("%s", st)

	// The two flows together must not exceed the link.
	if ratio := total.Bps() / float64(bw); ratio > 1.10 {
		t.Errorf("two flows delivered %.2f Mbps over a %.2f Mbps link (ratio %.3f)", total.Mbps(), float64(bw)/1e6, ratio)
	}
	if x == 0 || y == 0 {
		t.Fatalf("one flow was starved entirely: x=%d y=%d", x, y)
	}
	// Two identical greedy senders on one FIFO queue should land near an even
	// split. This is a property of the harness, not of any congestion control.
	if fairness < 0.90 {
		t.Errorf("Jain fairness %.3f between two identical greedy flows, want >= 0.90 (x=%d y=%d)", fairness, x, y)
	}
}

func TestNetworkCloseIsIdempotent(t *testing.T) {
	n := NewNetwork(16)
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{})
	n.Close()
	n.Close()
	if _, err := a.WriteTo([]byte("x"), b.Addr()); err == nil {
		t.Error("expected an error writing to a closed network")
	}
}

func TestDuplicateEndpointRejected(t *testing.T) {
	n := NewNetwork(17)
	defer n.Close()
	n.MustAddEndpoint("a")
	if _, err := n.AddEndpoint("a"); err == nil {
		t.Error("expected an error adding a duplicate endpoint name")
	}
}

func TestWriteToUnknownPeerFails(t *testing.T) {
	n := NewNetwork(18)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	other := &Peer{name: "nowhere"}
	if _, err := a.WriteTo([]byte("x"), other); err == nil {
		t.Error("expected ErrNoRoute writing to an unlinked peer")
	}
}

func TestFairnessIndex(t *testing.T) {
	cases := []struct {
		name  string
		rates []float64
		want  float64
	}{
		{"even split", []float64{50, 50}, 1.0},
		{"total starvation", []float64{100, 0}, 0.5},
		{"three even", []float64{10, 10, 10}, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FairnessIndex(tc.rates)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("FairnessIndex(%v) = %.4f, want %.4f", tc.rates, got, tc.want)
			}
		})
	}
	if got := FairnessIndex(nil); got != 0 {
		t.Errorf("FairnessIndex(nil) = %v, want 0", got)
	}
}
