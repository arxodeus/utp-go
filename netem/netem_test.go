package netem

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// The M1 gate. A harness you have not validated is worse than no harness: it
// produces numbers that look like measurements. These tests prove the three
// properties congestion results depend on -- configured bandwidth, configured
// delay and configured loss are what actually happens.

const testPayload = 1000

func makePayload(seq uint32, size int) []byte {
	b := make([]byte, size)
	binary.BigEndian.PutUint32(b, seq)
	return b
}

func payloadSeq(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// drainer reads from an endpoint in the background, recording what arrives.
//
// Endpoint.ReadFrom blocks until a packet arrives or the endpoint closes, so
// stopping means closing the endpoint. Snapshot any link stats you care about
// *before* calling stop: once the endpoint is closed, packets still in flight
// are counted as receiver-overflow drops.
type drainer struct {
	ep  *Endpoint
	wg  sync.WaitGroup
	mu  sync.Mutex
	got []uint32
}

func startDrain(ep *Endpoint) *drainer {
	d := &drainer{ep: ep, got: make([]uint32, 0, 4096)}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, _, err := ep.ReadFrom(buf)
			if err != nil {
				return
			}
			if n >= 4 {
				d.mu.Lock()
				d.got = append(d.got, payloadSeq(buf[:n]))
				d.mu.Unlock()
			}
		}
	}()
	return d
}

func (d *drainer) stop() {
	_ = d.ep.Close()
	d.wg.Wait()
}

func (d *drainer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.got)
}

func (d *drainer) seqs() []uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]uint32, len(d.got))
	copy(out, d.got)
	return out
}

// offerAtRate offers packets at `overload` times the given bit rate for the
// given duration, pacing in coarse bursts. Fine-grained tickers cannot pace
// reliably at sub-millisecond intervals, and a sender that accidentally
// offers *below* the bottleneck would make the bandwidth gate vacuous.
func offerAtRate(t *testing.T, src *Endpoint, dst *Endpoint, bps uint64, overload float64, dur time.Duration) time.Duration {
	t.Helper()
	const interval = 10 * time.Millisecond
	perInterval := int(float64(bps) / 8.0 / float64(testPayload) * interval.Seconds() * overload)
	if perInterval < 1 {
		perInterval = 1
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	start := time.Now()
	var seq uint32
	for time.Since(start) < dur {
		<-ticker.C
		for i := 0; i < perInterval; i++ {
			seq++
			if _, err := src.WriteTo(makePayload(seq, testPayload), dst.Addr()); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	return time.Since(start)
}

// --- Gate 1: a sender offering more than the link can carry achieves exactly
// the configured bandwidth, and no more. ------------------------------------

func TestGateBandwidthIsHonoured(t *testing.T) {
	for _, bw := range []uint64{2_000_000, 10_000_000} {
		t.Run(fmt.Sprintf("%dMbps", bw/1_000_000), func(t *testing.T) {
			n := NewNetwork(1)
			defer n.Close()
			a := n.MustAddEndpoint("a")
			b := n.MustAddEndpoint("b")
			n.Connect(a, b, Config{
				BandwidthBps: bw,
				QueueBytes:   32 * 1024,
			})

			d := startDrain(b)

			// Warm up first: offering at 2x fills the bottleneck queue, and
			// we want to measure the steady state, not the fill transient.
			offerAtRate(t, a, b, bw, 2.0, 700*time.Millisecond)

			// With the queue full and staying full, bytes delivered over the
			// window is exactly the link rate -- the backlog neither grows
			// nor drains, so there is no residue to correct for. Measuring
			// across the fill or the drain would overstate the rate.
			n.Link("a", "b").ResetStats()
			elapsed := offerAtRate(t, a, b, bw, 2.0, 2*time.Second)
			st := n.Link("a", "b").Stats()
			d.stop()

			got := Throughput{Bytes: st.BytesDelivered, Duration: elapsed}
			want := float64(bw)
			ratio := got.Bps() / want

			t.Logf("configured %.2f Mbps, delivered %s (ratio %.3f); %s",
				want/1e6, got, ratio, st)

			// Serialization is exact, so the only slack needed is for the
			// edges of the measurement window.
			if ratio < 0.90 || ratio > 1.10 {
				t.Errorf("delivered rate %.3f Mbps is %.1f%% of the configured %.3f Mbps, want within [90%%, 110%%]",
					got.Mbps(), ratio*100, want/1e6)
			}
			if st.DroppedByQueue == 0 {
				t.Errorf("offering at 2x the bottleneck produced no queue drops; the bottleneck is not limiting anything")
			}
		})
	}
}

// A sender offering *less* than the bottleneck must not be throttled: the
// link should be transparent below its rate.
func TestGateBandwidthDoesNotThrottleBelowRate(t *testing.T) {
	n := NewNetwork(2)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{BandwidthBps: 10_000_000, QueueBytes: 64 * 1024})

	d := startDrain(b)

	const packets = 500
	for i := 0; i < packets; i++ {
		if _, err := a.WriteTo(makePayload(uint32(i), testPayload), b.Addr()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // 4 Mbps, well under the 10 Mbps link
	}
	time.Sleep(300 * time.Millisecond)
	st := n.Link("a", "b").Stats()
	d.stop()
	t.Logf("%s", st)
	if st.PacketsDropped != 0 {
		t.Errorf("under-rate sender lost %d packets, want 0", st.PacketsDropped)
	}
	if st.PacketsDelivered != packets {
		t.Errorf("delivered %d packets, want %d", st.PacketsDelivered, packets)
	}
}

// --- Gate 2: a configured one-way delay is the delay actually observed. -----

func TestGateDelayIsHonoured(t *testing.T) {
	for _, want := range []time.Duration{20 * time.Millisecond, 100 * time.Millisecond} {
		t.Run(want.String(), func(t *testing.T) {
			n := NewNetwork(3)
			defer n.Close()
			a := n.MustAddEndpoint("a")
			b := n.MustAddEndpoint("b")
			// No bandwidth limit: isolate propagation from serialization.
			n.Connect(a, b, Config{Delay: want})

			const samples = 20
			observed := make([]time.Duration, 0, samples)
			buf := make([]byte, 65535)
			for i := 0; i < samples; i++ {
				sent := time.Now()
				if _, err := a.WriteTo(makePayload(uint32(i), testPayload), b.Addr()); err != nil {
					t.Fatal(err)
				}
				if _, _, err := b.ReadFrom(buf); err != nil {
					t.Fatal(err)
				}
				observed = append(observed, time.Since(sent))
			}

			var sum time.Duration
			min, max := observed[0], observed[0]
			for _, d := range observed {
				sum += d
				if d < min {
					min = d
				}
				if d > max {
					max = d
				}
			}
			mean := sum / time.Duration(len(observed))
			t.Logf("configured %v: observed mean %v (min %v, max %v over %d samples)",
				want, mean.Round(time.Microsecond), min.Round(time.Microsecond), max.Round(time.Microsecond), samples)

			// The emulator can only add delay, never remove it, so the mean
			// must sit just above the configured value. Scheduler wake-up
			// latency is the only source of the excess.
			if mean < want {
				t.Errorf("observed mean %v is below the configured %v -- the delay is not being applied", mean, want)
			}
			if slack := mean - want; slack > 5*time.Millisecond {
				t.Errorf("observed mean %v exceeds configured %v by %v, more than the 5ms scheduler allowance", mean, want, slack)
			}
		})
	}
}

// Round-trip delay across a symmetric link must be twice the one-way delay:
// this is the RTT a congestion controller will measure.
func TestGateRoundTripIsTwiceOneWay(t *testing.T) {
	const oneWay = 25 * time.Millisecond
	n := NewNetwork(4)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: oneWay})

	// Echo server on b.
	go func() {
		buf := make([]byte, 65535)
		for {
			nb, from, err := b.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := b.WriteTo(buf[:nb], from); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, 65535)
	var sum time.Duration
	const samples = 15
	for i := 0; i < samples; i++ {
		sent := time.Now()
		if _, err := a.WriteTo(makePayload(uint32(i), testPayload), b.Addr()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
		sum += time.Since(sent)
	}
	mean := sum / samples
	want := 2 * oneWay
	t.Logf("one-way %v configured each direction: observed RTT mean %v (want ~%v)", oneWay, mean.Round(time.Microsecond), want)
	if mean < want {
		t.Errorf("observed RTT %v below %v", mean, want)
	}
	if mean-want > 8*time.Millisecond {
		t.Errorf("observed RTT %v exceeds %v by more than 8ms", mean, want)
	}
}

// --- Gate 3: a configured loss rate is the loss rate actually observed. -----

func TestGateLossRateIsHonoured(t *testing.T) {
	for _, want := range []float64{0.01, 0.05} {
		t.Run(fmt.Sprintf("%.0f%%", want*100), func(t *testing.T) {
			n := NewNetwork(5)
			defer n.Close()
			a := n.MustAddEndpoint("a")
			b := n.MustAddEndpoint("b")
			// No bandwidth limit, so the only drop cause is medium loss.
			n.Connect(a, b, Config{LossRate: want})

			d := startDrain(b)

			const offered = 40000
			for i := 0; i < offered; i++ {
				if _, err := a.WriteTo(makePayload(uint32(i), 64), b.Addr()); err != nil {
					t.Fatal(err)
				}
				// Pace lightly: an unpaced blast overflows the reader's inbox,
				// which is a real drop but not the one under test here.
				if i%500 == 499 {
					time.Sleep(2 * time.Millisecond)
				}
			}
			time.Sleep(500 * time.Millisecond)
			st := n.Link("a", "b").Stats()
			d.stop()
			got := st.MediumLossRate()
			t.Logf("configured loss %.3f, observed %.5f over %d packets; %s", want, got, offered, st)

			// 3 sigma on a binomial with n=40000.
			sigma := math.Sqrt(want * (1 - want) / float64(offered))
			tol := 3 * sigma
			if math.Abs(got-want) > tol {
				t.Errorf("observed loss %.5f differs from configured %.5f by more than 3 sigma (%.5f)", got, want, tol)
			}
			if st.DroppedByQueue != 0 {
				t.Errorf("unlimited-bandwidth link reported %d queue drops", st.DroppedByQueue)
			}
		})
	}
}

func TestGateZeroLossDeliversEverything(t *testing.T) {
	n := NewNetwork(6)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: time.Millisecond})

	d := startDrain(b)

	const offered = 2000
	for i := 0; i < offered; i++ {
		if _, err := a.WriteTo(makePayload(uint32(i), 128), b.Addr()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	st := n.Link("a", "b").Stats()
	delivered := d.count()
	d.stop()
	t.Logf("%s", st)
	if delivered != offered {
		t.Errorf("perfect link delivered %d of %d packets", delivered, offered)
	}
	if st.PacketsDropped != 0 {
		t.Errorf("perfect link dropped %d packets", st.PacketsDropped)
	}
}
