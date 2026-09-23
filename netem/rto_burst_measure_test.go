//go:build cgo

package netem

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// What each sender puts on the wire at a retransmission timeout.
//
// A 16MB transfer over a 10ms, 20 Mb/s link, with the data direction blacked
// out for 3 seconds starting 1 second in. Every data packet offered to the
// link during the blackout is logged, grouped into bursts. The receiver is
// ours in both modes; only the sender differs.
//
// Observed (see KNOWN-LIMITATIONS.md, section 2f):
//
//	libutp:        one packet at the timeout (2.0-2.5s) -- the oldest -- and
//	               nothing more before the blackout ends.
//	ours, before:  the whole window, 75 packets, at ~2.0s, out of order;
//	               then 48-66 of them again at ~3.0s, without the backoff
//	               doubling. Three runs.
//	ours, after:   one packet, the oldest, at 2.04-2.06s, and nothing more.
//	               Two runs.
//
// libutp marks every packet in flight need_resend and resends only the
// oldest (utp_internal.cpp:1230-1252); the rest wait for flush_packets and
// the congestion window, or come back one per acknowledgement through the
// fast-timeout retry. The regression test for that is
// TestRetransmissionTimeoutResendsOnlyTheOldest, which is deterministic. This
// is the measurement behind it, and it takes about 25 seconds, so it runs
// only when UTP_RTO_BURST_MEASURE is set.
func TestRTOBurstMeasure(t *testing.T) {
	if os.Getenv("UTP_RTO_BURST_MEASURE") == "" {
		t.Skip("measurement; set UTP_RTO_BURST_MEASURE=1 to run")
	}
	for _, mode := range []string{"libutp->go", "go->go"} {
		t.Run(mode, func(t *testing.T) { measureRTOBursts(t, mode) })
	}
}

type offered struct {
	at    time.Duration
	seq   uint16
	retx  bool
	bytes int
}

func measureRTOBursts(t *testing.T, mode string) {
	const (
		blackoutAt  = 1 * time.Second
		blackoutFor = 3 * time.Second
	)
	n := NewNetwork(31)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	base := Config{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000}

	var mu sync.Mutex
	var start time.Time
	var log []offered
	seen := map[uint16]bool{}
	fwd := base
	fwd.OnOffered = func(p []byte, at time.Time) {
		h, err := utp.DecodePacketHeader(p)
		if err != nil || h.PacketType.String() != "st_data" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if start.IsZero() {
			start = at
		}
		log = append(log, offered{at: at.Sub(start), seq: h.SeqNum, retx: seen[h.SeqNum], bytes: len(p)})
		seen[h.SeqNum] = true
	}
	n.ConnectAsymmetric(a, b, fwd, base)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	go func() {
		// Timed from the first data packet, not from here.
		for {
			mu.Lock()
			s := start
			mu.Unlock()
			if !s.IsZero() {
				time.Sleep(time.Until(s.Add(blackoutAt)))
				break
			}
			time.Sleep(time.Millisecond)
		}
		dark := fwd
		dark.LossRate = 1
		_ = n.SetConfig("a", "b", dark)
		time.Sleep(blackoutFor)
		_ = n.SetConfig("a", "b", fwd)
	}()

	payload := make([]byte, 16<<20)
	var err error
	switch mode {
	case "libutp->go":
		_, _, err = libutpToGo(ctx, n, a, b, payload, 7000)
	case "go->go":
		pair := NewUtpPair(ctx, n, a, b, quiet())
		_, err = pair.RunTransfer(ctx, payload, FlowOptions{InitiatorCid: 100})
	}
	if err != nil {
		t.Fatalf("%s: %v", mode, err)
	}

	mu.Lock()
	defer mu.Unlock()
	sort.Slice(log, func(i, j int) bool { return log[i].at < log[j].at })
	// Bursts during the blackout: packets within 5ms of the previous one.
	var lines []string
	var burst []offered
	flush := func() {
		if len(burst) == 0 {
			return
		}
		retx := 0
		for _, o := range burst {
			if o.retx {
				retx++
			}
		}
		lines = append(lines, fmt.Sprintf("  +%7.3fs  %3d packets (%3d retransmissions), seqs %d..%d",
			burst[0].at.Seconds(), len(burst), retx, burst[0].seq, burst[len(burst)-1].seq))
		burst = nil
	}
	for _, o := range log {
		if o.at < blackoutAt || o.at >= blackoutAt+blackoutFor {
			continue
		}
		if len(burst) > 0 && o.at-burst[len(burst)-1].at > 5*time.Millisecond {
			flush()
		}
		burst = append(burst, o)
	}
	flush()
	t.Logf("%s: data packets offered during the blackout (%v to %v):\n%s",
		mode, blackoutAt, blackoutAt+blackoutFor, strings.Join(lines, "\n"))
}
