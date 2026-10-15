//go:build cgo

package netem

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"sync"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// TestLibutpDeferenceToLossBasedFlow puts libutp through
// TestDeferenceToLossBasedFlow's experiment: the same shared bottleneck, the
// same loss-based competitor with the same head start, the same 4 MB, with
// libutp at both ends of the uTP flow. This library's classic LEDBAT runs the
// same experiment alongside it in each run, so the two are compared under the
// same conditions rather than against a figure taken another day.
//
// libutp's classic LEDBAT is what this library's default is meant to match,
// and "less than best effort" is the claim it is judged on. Until this ran,
// what libutp itself yields was assumed, not measured.
func TestLibutpDeferenceToLossBasedFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	solo := runRenoSolo(t)
	t.Logf("baseline: the competitor alone gets %.2f Mbps", solo)

	impls := []string{"libutp", "ours"}
	if os.Getenv("UTP_DEFERENCE_MIXED") != "" {
		// The two mixed pairings, to put a difference on the end that makes
		// it: the sender's controller, or the receiver's acknowledgements,
		// which here share the bottleneck with the competitor's data.
		impls = append(impls, "libutp->ours", "ours->libutp")
	}
	for _, impl := range impls {
		var utpMbps, renoMbps, share float64
		switch impl {
		case "libutp":
			utpMbps, renoMbps, share = runLibutpDeference(t)
		case "ours":
			utpMbps, renoMbps, _, share = runDeference(t, utp.AlgorithmLEDBAT)
		default:
			utpMbps, renoMbps = runMixedDeference(t, impl == "libutp->ours")
		}
		t.Logf("%s: uTP %.2f Mbps, competitor %.2f Mbps", impl, utpMbps, renoMbps)
		t.Logf("%s: the competitor kept %.0f%% of what it got alone (%.2f of %.2f Mbps)",
			impl, 100*renoMbps/solo, renoMbps, solo)
		if share > 0 {
			t.Logf("%s: uTP's share while both ran %.1f%%", impl, 100*share)
		}
		if utpMbps <= 0 || renoMbps <= 0 {
			t.Errorf("%s: a flow got no bandwidth at all; nothing can be concluded", impl)
		}
	}
}

// runMixedDeference is the experiment with libutp at one end of the uTP flow
// and this library at the other.
func runMixedDeference(t *testing.T, libutpSends bool) (utpMbps, renoMbps float64) {
	t.Helper()
	n := NewNetwork(311)
	defer n.Close()
	utpSender := n.MustAddEndpoint("utp-sender")
	utpReceiver := n.MustAddEndpoint("utp-receiver")
	renoSender := n.MustAddEndpoint("reno-sender")
	renoReceiver := n.MustAddEndpoint("reno-receiver")
	n.ConnectShared(deferenceLinkConfig(),
		[2]*Endpoint{utpSender, utpReceiver},
		[2]*Endpoint{utpReceiver, utpSender},
		[2]*Endpoint{renoSender, renoReceiver},
		[2]*Endpoint{renoReceiver, renoSender},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	data := make([]byte, deferencePayload)
	for i := range data {
		data[i] = byte(i)
	}
	var (
		wg      sync.WaitGroup
		renoRes *RenoResult
		renoErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		renoRes, renoErr = RunRenoFlow(ctx, renoSender, renoReceiver, deferencePayload, 1024)
	}()
	time.Sleep(1500 * time.Millisecond)
	var (
		elapsed time.Duration
		got     []byte
		err     error
	)
	if libutpSends {
		elapsed, got, err = libutpToGo(ctx, n, utpSender, utpReceiver, data, 9200)
	} else {
		elapsed, got, err = goToLibutp(ctx, n, utpSender, utpReceiver, data, 9200)
	}
	wg.Wait()
	if err != nil {
		t.Fatalf("uTP flow: %v", err)
	}
	if renoErr != nil {
		t.Fatalf("competitor: %v", renoErr)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("uTP flow delivered %d bytes, not the payload", len(got))
	}
	return float64(len(data)) * 8 / elapsed.Seconds() / 1e6, renoRes.Goodput.Mbps()
}

// runLibutpDeference is runDeference with libutp sending to libutp.
func runLibutpDeference(t *testing.T) (utpMbps, renoMbps, utpShare float64) {
	t.Helper()

	n := NewNetwork(311)
	defer n.Close()
	utpSender := n.MustAddEndpoint("utp-sender")
	utpReceiver := n.MustAddEndpoint("utp-receiver")
	renoSender := n.MustAddEndpoint("reno-sender")
	renoReceiver := n.MustAddEndpoint("reno-receiver")
	cfg := deferenceLinkConfig()
	n.ConnectShared(cfg,
		[2]*Endpoint{utpSender, utpReceiver},
		[2]*Endpoint{utpReceiver, utpSender},
		[2]*Endpoint{renoSender, renoReceiver},
		[2]*Endpoint{renoReceiver, renoSender},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	data := make([]byte, deferencePayload)
	for i := range data {
		data[i] = byte(i)
	}

	var (
		wg            sync.WaitGroup
		renoRes       *RenoResult
		renoErr       error
		renoDelivered []DeliverySample
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		renoRes, renoErr = RunRenoFlow(ctx, renoSender, renoReceiver, deferencePayload, 1024)
	}()

	time.Sleep(1500 * time.Millisecond)

	const connSeed = 9100
	sender, err := newLibutpEndpoint(utpSender, utpReceiver.Addr(), connSeed, libutpSender)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	sender.payload = data
	receiver, err := newLibutpEndpoint(utpReceiver, utpSender.Addr(), connSeed+1000, libutpReceiver)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	receiver.expect = len(data)
	receiver.linger = true

	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	var recvWG sync.WaitGroup
	var recvErr error
	recvWG.Add(1)
	go func() {
		defer recvWG.Done()
		_, recvErr = receiver.run(rctx)
	}()
	_, sendErr := sender.run(ctx)
	time.Sleep(50 * time.Millisecond)
	rcancel()
	recvWG.Wait()
	wg.Wait()

	if sendErr != nil {
		t.Fatalf("libutp sender: %v", sendErr)
	}
	if recvErr != nil {
		t.Fatalf("libutp receiver: %v", recvErr)
	}
	if renoErr != nil {
		t.Fatalf("competitor: %v", renoErr)
	}
	if receiver.doneAt == 0 || len(receiver.received) != len(data) {
		t.Fatalf("libutp receiver got %d of %d bytes", len(receiver.received), len(data))
	}
	for i := range data {
		if receiver.received[i] != data[i] {
			t.Fatalf("libutp received the wrong bytes at offset %d", i)
		}
	}
	elapsed := receiver.start.Add(receiver.doneAt).Sub(sender.start)
	utpMbps = float64(len(data)) * 8 / elapsed.Seconds() / 1e6
	renoMbps = renoRes.Goodput.Mbps()
	renoDelivered = renoRes.Delivered
	share, ok := SplitWhileBothRan(receiver.delivered.snapshot(), renoDelivered)
	if !ok {
		t.Errorf("the two flows never overlapped; there is no split to report")
	}
	return utpMbps, renoMbps, share
}

// A receiver's acknowledgements that queue behind another flow's data are
// not its own queue, and coalescing them cost the sender's yielding: libutp's
// sender, sharing a bottleneck with a loss-based flow, left it 64.0% of its
// rate with this receiver coalescing to a third of libutp's acknowledgements,
// against 67.5% with libutp's receiver. This receiver now coalesces only
// once the way back climbs as its own acknowledgements make it climb
// (ackPathSelfQueued), and here acknowledges as libutp does. Measured: 0.95-
// 0.98 acknowledgements per data packet, libutp's 0.97; 0.35-0.41 before.
func TestReceiverDoesNotCoalesceBehindAnotherFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	var mu sync.Mutex
	var data, acks int
	cfg := deferenceLinkConfig()
	cfg.OnOffered = func(p []byte, _ time.Time) {
		if len(p) < 20 || p[0]&0x0f != 1 {
			return // the competitor's packets are not uTP
		}
		if cid := binary.BigEndian.Uint16(p[2:4]); cid < 9300 || cid > 9301 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch p[0] >> 4 {
		case 0: // ST_DATA
			data++
		case 2: // ST_STATE
			acks++
		}
	}
	n := NewNetwork(311)
	defer n.Close()
	us := n.MustAddEndpoint("utp-sender")
	ur := n.MustAddEndpoint("utp-receiver")
	rs := n.MustAddEndpoint("reno-sender")
	rr := n.MustAddEndpoint("reno-receiver")
	n.ConnectShared(cfg, [2]*Endpoint{us, ur}, [2]*Endpoint{ur, us}, [2]*Endpoint{rs, rr}, [2]*Endpoint{rr, rs})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = RunRenoFlow(ctx, rs, rr, deferencePayload, 1024)
	}()
	time.Sleep(1500 * time.Millisecond)
	payload := make([]byte, deferencePayload)
	if _, _, err := libutpToGo(ctx, n, us, ur, payload, 9300); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	ratio := float64(acks) / float64(data)
	t.Logf("%d data packets drew %d acknowledgements (%.2f each); libutp's receiver sends about 0.97", data, acks, ratio)
	if ratio < 0.8 {
		t.Errorf("%.2f acknowledgements per data packet behind another flow's queue; this receiver is "+
			"coalescing where its acknowledgements are not the queue", ratio)
	}
}
