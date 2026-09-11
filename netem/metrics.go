package netem

import (
	"fmt"
	"sort"
	"time"
)

// Stats is what one Link observed. All counters are cumulative.
type Stats struct {
	// Offered is what the sender handed to the link.
	PacketsOffered uint64
	BytesOffered   uint64

	// Delivered is what reached the destination's inbox.
	PacketsDelivered uint64
	BytesDelivered   uint64

	// Dropped is the total, broken down by cause below.
	PacketsDropped    uint64
	DroppedByLoss     uint64
	DroppedByQueue    uint64
	DroppedByReceiver uint64
	// DroppedByMTU counts datagrams larger than the link's MTU. This is a
	// statement about size, not about congestion, and is kept apart from the
	// other two for that reason.
	DroppedByMTU uint64

	PacketsReordered uint64

	QueueDelaySum   time.Duration
	QueueDelayCount uint64
	QueueDelayMax   time.Duration
}

// LossRate is the fraction of offered packets that did not arrive.
func (s Stats) LossRate() float64 {
	if s.PacketsOffered == 0 {
		return 0
	}
	return float64(s.PacketsDropped) / float64(s.PacketsOffered)
}

// MediumLossRate is the fraction dropped by Config.LossRate alone, excluding
// congestion and receiver overflow. This is the number to compare against the
// configured loss rate.
func (s Stats) MediumLossRate() float64 {
	if s.PacketsOffered == 0 {
		return 0
	}
	return float64(s.DroppedByLoss) / float64(s.PacketsOffered)
}

// MeanQueueDelay is the average time packets waited for the bottleneck.
func (s Stats) MeanQueueDelay() time.Duration {
	if s.QueueDelayCount == 0 {
		return 0
	}
	return s.QueueDelaySum / time.Duration(s.QueueDelayCount)
}

// String renders the stats for a test log.
func (s Stats) String() string {
	return fmt.Sprintf(
		"offered=%d/%dB delivered=%d/%dB dropped=%d (loss=%d queue=%d rcvr=%d mtu=%d) reordered=%d qdelay mean=%v max=%v",
		s.PacketsOffered, s.BytesOffered,
		s.PacketsDelivered, s.BytesDelivered,
		s.PacketsDropped, s.DroppedByLoss, s.DroppedByQueue, s.DroppedByReceiver, s.DroppedByMTU,
		s.PacketsReordered, s.MeanQueueDelay(), s.QueueDelayMax)
}

// QueueSample is one observation of the bottleneck's state.
type QueueSample struct {
	At           time.Time
	Delay        time.Duration
	BacklogBytes int
}

// Stats returns a snapshot of this link's counters.
func (l *Link) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

// QueueSamples returns the queueing-delay time series, oldest first.
// This is the standing-queue measurement a delay-based controller is judged
// on: LEDBAT is supposed to keep this bounded near its target.
func (l *Link) QueueSamples() []QueueSample {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]QueueSample, len(l.samples))
	copy(out, l.samples)
	return out
}

// QueueDelayPercentile returns the p'th percentile (0..1) of observed
// queueing delay. The median and p95 say far more about a controller's
// behaviour than the mean, which a long idle tail flatters.
func (l *Link) QueueDelayPercentile(p float64) time.Duration {
	l.mu.Lock()
	samples := make([]time.Duration, len(l.samples))
	for i, s := range l.samples {
		samples[i] = s.Delay
	}
	l.mu.Unlock()
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	idx := int(p * float64(len(samples)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}

// ResetStats clears counters and samples, for measuring a steady-state window
// after warm-up rather than including the handshake.
func (l *Link) ResetStats() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stats = Stats{}
	l.samples = nil
}

// EndpointStats is what an Endpoint observed on its receive side.
type EndpointStats struct {
	PacketsReceived uint64
	BytesReceived   uint64
	// PacketsDroppedFullInbox is packets that arrived while the application
	// was not reading fast enough.
	PacketsDroppedFullInbox uint64
}

// Stats returns a snapshot of this endpoint's receive counters.
func (e *Endpoint) Stats() EndpointStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EndpointStats{
		PacketsReceived:         e.received,
		BytesReceived:           e.rxBytes,
		PacketsDroppedFullInbox: e.dropped,
	}
}

// Throughput is a goodput measurement over a wall-clock interval.
type Throughput struct {
	Bytes    uint64
	Duration time.Duration
}

// Bps returns the rate in bits per second.
func (t Throughput) Bps() float64 {
	if t.Duration <= 0 {
		return 0
	}
	return float64(t.Bytes) * 8 / t.Duration.Seconds()
}

// Mbps returns the rate in megabits per second.
func (t Throughput) Mbps() float64 { return t.Bps() / 1e6 }

// String renders the throughput for a test log.
func (t Throughput) String() string {
	return fmt.Sprintf("%.2f Mbps (%d bytes in %v)", t.Mbps(), t.Bytes, t.Duration.Round(time.Millisecond))
}

// FairnessIndex returns Jain's fairness index for a set of flow rates: 1.0 is
// a perfectly even split, 1/n is one flow taking everything. It is the
// standard way to report how two flows shared a bottleneck.
func FairnessIndex(rates []float64) float64 {
	if len(rates) == 0 {
		return 0
	}
	var sum, sumSq float64
	for _, r := range rates {
		sum += r
		sumSq += r * r
	}
	if sumSq == 0 {
		return 0
	}
	return (sum * sum) / (float64(len(rates)) * sumSq)
}
