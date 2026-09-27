package netem

import (
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// DeliverySample is a flow's cumulative delivered byte count at a moment.
type DeliverySample struct {
	At    time.Time
	Bytes uint64
}

// deliverySampleInterval bounds how often a deliveryTimeline records.
const deliverySampleInterval = 5 * time.Millisecond

// deliveryTimeline records a DeliverySample at most every
// deliverySampleInterval, and always the latest count.
type deliveryTimeline struct {
	mu      sync.Mutex
	samples []DeliverySample
}

func (d *deliveryTimeline) record(at time.Time, bytes uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := len(d.samples); n > 0 && at.Sub(d.samples[n-1].At) < deliverySampleInterval {
		// Keep the latest count, so the series ends at the true total.
		d.samples[n-1].Bytes = bytes
		return
	}
	d.samples = append(d.samples, DeliverySample{At: at, Bytes: bytes})
}

func (d *deliveryTimeline) snapshot() []DeliverySample {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DeliverySample(nil), d.samples...)
}

// ReceivedSeries converts a receiver's recorded metrics into a delivery
// series, from the bytes it accepted from its peer.
func ReceivedSeries(samples []utp.ConnectionMetrics) []DeliverySample {
	out := make([]DeliverySample, 0, len(samples))
	for _, m := range samples {
		out = append(out, DeliverySample{At: m.At, Bytes: m.BytesReceived})
	}
	return out
}

// SplitWhileBothRan is a's share of the bytes the two flows delivered while
// both were running: from the later flow's first sample to the earlier
// flow's last. ok is false if they never overlapped or delivered nothing in
// the overlap.
//
// This is the split itself. A share or a Jain index taken from each flow's
// whole-transfer goodput is not: a flow squeezed while both run finishes
// later, with the link to itself for the rest, and its goodput catches up.
func SplitWhileBothRan(a, b []DeliverySample) (aShare float64, ok bool) {
	if len(a) == 0 || len(b) == 0 {
		return 0, false
	}
	from := a[0].At
	if b[0].At.After(from) {
		from = b[0].At
	}
	until := a[len(a)-1].At
	if b[len(b)-1].At.Before(until) {
		until = b[len(b)-1].At
	}
	if !until.After(from) {
		return 0, false
	}
	da := float64(bytesAt(a, until) - bytesAt(a, from))
	db := float64(bytesAt(b, until) - bytesAt(b, from))
	if da+db == 0 {
		return 0, false
	}
	return da / (da + db), true
}

// bytesAt is the count as of t: the last sample at or before it.
func bytesAt(s []DeliverySample, t time.Time) uint64 {
	var v uint64
	for _, m := range s {
		if m.At.After(t) {
			break
		}
		v = m.Bytes
	}
	return v
}
