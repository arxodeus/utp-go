package netem

import (
	"fmt"
	"sort"
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// Recorder collects a connection's metric snapshots into a time series.
//
// Use its Observe method as a utp_go.ConnectionConfig.Metrics observer. It
// only appends under a mutex, so it satisfies the requirement that an
// observer must not block the connection's event loop.
type Recorder struct {
	mu      sync.Mutex
	samples []utp.ConnectionMetrics
	limit   int
}

// DefaultRecorderLimit caps retained samples so a long soak cannot exhaust
// memory. Once reached, the recorder keeps the first and last halves and
// drops the middle, which preserves start-up and steady-state behaviour.
const DefaultRecorderLimit = 200_000

// NewRecorder returns a recorder with the default sample limit.
func NewRecorder() *Recorder { return &Recorder{limit: DefaultRecorderLimit} }

// Observe records one snapshot. It satisfies utp_go.MetricsObserver.
func (r *Recorder) Observe(m utp.ConnectionMetrics) {
	r.mu.Lock()
	if r.limit > 0 && len(r.samples) >= r.limit {
		half := len(r.samples) / 2
		r.samples = append(r.samples[:half:half], r.samples[half+1:]...)
	}
	r.samples = append(r.samples, m)
	r.mu.Unlock()
}

// Samples returns a copy of the recorded series, oldest first.
func (r *Recorder) Samples() []utp.ConnectionMetrics {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]utp.ConnectionMetrics, len(r.samples))
	copy(out, r.samples)
	return out
}

// Len returns how many samples have been recorded.
func (r *Recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.samples)
}

// Last returns the most recent snapshot, and whether there was one.
func (r *Recorder) Last() (utp.ConnectionMetrics, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.samples) == 0 {
		return utp.ConnectionMetrics{}, false
	}
	return r.samples[len(r.samples)-1], true
}

// Summary condenses a recorded series into the numbers the M5 gates report.
type Summary struct {
	Samples int
	Span    time.Duration

	CwndMeanBytes uint64
	CwndMaxBytes  uint32
	CwndMinBytes  uint32
	// CwndPinnedAtMin is the fraction of samples where the window sat at its
	// floor. A controller stuck here has collapsed rather than backed off.
	CwndPinnedAtMin float64

	RTTMean time.Duration
	RTTP50  time.Duration
	RTTP95  time.Duration
	RTTMax  time.Duration
	// RTTMin is the lowest round trip seen over the whole flow. On a path
	// this flow started on while it was empty, this is the path's real
	// unloaded round trip.
	RTTMin time.Duration

	// StandingQueueP50 and StandingQueueP95 are the round trip above
	// RTTMin -- the queue this flow actually left standing on the path.
	//
	// This exists because QueueingDelay below cannot be trusted for that
	// question. QueueingDelay is `PeerTsDiff - BaseDelay`, and BaseDelay is
	// the *controller's own estimate* of the empty path. A delay-based
	// controller whose base-delay estimate has drifted upwards -- which is
	// precisely the failure LEDBAT++ exists to fix -- reports a small
	// queueing delay while sitting on a large queue, because it is
	// subtracting the queue from itself.
	//
	// Measured on a 40ms path: classic LEDBAT reported 156µs of queueing
	// delay while its round trip sat at 80ms. It was causing 40ms of queue
	// and could not see it. StandingQueueP50 reports the 40ms.
	StandingQueueP50 time.Duration
	StandingQueueP95 time.Duration

	// Queueing delay is the excess of measured one-way delay over the lowest
	// seen: the standing queue LEDBAT exists to bound.
	QueueingDelayMean time.Duration
	QueueingDelayP50  time.Duration
	QueueingDelayP95  time.Duration
	QueueingDelayMax  time.Duration

	BytesSent            uint64
	PacketsSent          uint64
	PacketsRetransmitted uint64
	RetransmitRate       float64
	Timeouts             uint64
	FastRetransmits      uint64
}

// String renders the summary for a test log.
func (s Summary) String() string {
	return fmt.Sprintf(
		"cwnd mean=%dB max=%dB min=%dB pinned=%.1f%% | rtt min=%v p50=%v p95=%v max=%v | standing queue p50=%v p95=%v | qdelay(believed) p50=%v p95=%v max=%v | sent=%d pkts retx=%d (%.2f%%) timeouts=%d fastretx=%d over %v",
		s.CwndMeanBytes, s.CwndMaxBytes, s.CwndMinBytes, s.CwndPinnedAtMin*100,
		s.RTTMin.Round(time.Microsecond),
		s.RTTP50.Round(time.Microsecond), s.RTTP95.Round(time.Microsecond), s.RTTMax.Round(time.Microsecond),
		s.StandingQueueP50.Round(time.Microsecond), s.StandingQueueP95.Round(time.Microsecond),
		s.QueueingDelayP50.Round(time.Microsecond), s.QueueingDelayP95.Round(time.Microsecond), s.QueueingDelayMax.Round(time.Microsecond),
		s.PacketsSent, s.PacketsRetransmitted, s.RetransmitRate*100, s.Timeouts, s.FastRetransmits,
		s.Span.Round(time.Millisecond))
}

func percentileDur(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Summary condenses the recorded series.
//
// Samples taken before the connection is established carry no controller
// state, so they are excluded from the congestion statistics; including them
// would drag every average towards zero.
func (r *Recorder) Summary() Summary {
	samples := r.Samples()
	var s Summary
	if len(samples) == 0 {
		return s
	}
	s.Samples = len(samples)
	s.Span = samples[len(samples)-1].At.Sub(samples[0].At)

	rtts := make([]time.Duration, 0, len(samples))
	qdelays := make([]time.Duration, 0, len(samples))
	var cwndSum uint64
	var cwndCount uint64
	var pinned uint64

	for _, m := range samples {
		if m.CwndBytes > 0 {
			cwndSum += uint64(m.CwndBytes)
			cwndCount++
			if m.CwndBytes > s.CwndMaxBytes {
				s.CwndMaxBytes = m.CwndBytes
			}
			if s.CwndMinBytes == 0 || m.CwndBytes < s.CwndMinBytes {
				s.CwndMinBytes = m.CwndBytes
			}
			if m.MinCwndBytes > 0 && m.CwndBytes <= m.MinCwndBytes {
				pinned++
			}
		}
		if m.RTT > 0 {
			rtts = append(rtts, m.RTT)
			if m.RTT > s.RTTMax {
				s.RTTMax = m.RTT
			}
			if s.RTTMin == 0 || m.RTT < s.RTTMin {
				s.RTTMin = m.RTT
			}
		}
		if q := m.QueueingDelay(); q > 0 {
			qdelays = append(qdelays, q)
			if q > s.QueueingDelayMax {
				s.QueueingDelayMax = q
			}
		}
	}

	if cwndCount > 0 {
		s.CwndMeanBytes = cwndSum / cwndCount
		s.CwndPinnedAtMin = float64(pinned) / float64(cwndCount)
	}

	if len(rtts) > 0 {
		var sum time.Duration
		for _, d := range rtts {
			sum += d
		}
		s.RTTMean = sum / time.Duration(len(rtts))
		sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
		s.RTTP50 = percentileDur(rtts, 0.50)
		s.RTTP95 = percentileDur(rtts, 0.95)
		if s.RTTP50 > s.RTTMin {
			s.StandingQueueP50 = s.RTTP50 - s.RTTMin
		}
		if s.RTTP95 > s.RTTMin {
			s.StandingQueueP95 = s.RTTP95 - s.RTTMin
		}
	}

	if len(qdelays) > 0 {
		var sum time.Duration
		for _, d := range qdelays {
			sum += d
		}
		s.QueueingDelayMean = sum / time.Duration(len(qdelays))
		sort.Slice(qdelays, func(i, j int) bool { return qdelays[i] < qdelays[j] })
		s.QueueingDelayP50 = percentileDur(qdelays, 0.50)
		s.QueueingDelayP95 = percentileDur(qdelays, 0.95)
	}

	last := samples[len(samples)-1]
	s.BytesSent = last.BytesSent
	s.PacketsSent = last.PacketsSent
	s.PacketsRetransmitted = last.PacketsRetransmitted
	s.RetransmitRate = last.RetransmitRate()
	s.Timeouts = last.Timeouts
	s.FastRetransmits = last.FastRetransmits
	return s
}

// CwndSeries returns the congestion window over time, for plotting or for
// asserting that the window actually moves.
func (r *Recorder) CwndSeries() ([]time.Duration, []uint32) {
	samples := r.Samples()
	if len(samples) == 0 {
		return nil, nil
	}
	start := samples[0].At
	ts := make([]time.Duration, 0, len(samples))
	vals := make([]uint32, 0, len(samples))
	for _, m := range samples {
		ts = append(ts, m.At.Sub(start))
		vals = append(vals, m.CwndBytes)
	}
	return ts, vals
}
