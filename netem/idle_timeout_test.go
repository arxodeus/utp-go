package netem

import (
	"context"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	elog "github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

// A connection that goes quiet must not time out.
//
// Noticed in the trace of TestApplicationLimitedWindowDoesNotGrow: on a link
// with no loss configured, a connection that finished a bulk transfer and then
// trickled small writes took retransmission timeouts, and each one collapsed
// the congestion window to the floor. Nothing was lost, so nothing should have
// timed out.
//
// This is the shape a BitTorrent peer connection spends most of its life in --
// a burst of blocks, then quiet, then a request. If a quiet connection times
// out, every peer pays a window collapse for having been idle.
//
// libutp only acts on the retransmission timeout when there is something
// outstanding to retransmit: `check_timeouts` requires `cur_window_packets > 0`
// before it treats the deadline as a loss (utp_internal.cpp:1147-1155).
func TestQuietConnectionDoesNotTimeOut(t *testing.T) {
	n := NewNetwork(6060)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	// A generous queue and no loss: nothing on this link can legitimately
	// cause a retransmission.
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   512 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sockA := utp.WithSocket(ctx, a, debugIfSet())
	defer sockA.Close()
	sockB := utp.WithSocket(ctx, b, quiet())
	defer sockB.Close()

	var (
		mu      sync.Mutex
		samples []utp.ConnectionMetrics
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 20 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		samples = append(samples, m)
		mu.Unlock()
	}

	go func() {
		stream, err := sockB.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		buf := make([]byte, 64*1024)
		for ctx.Err() == nil {
			if _, err := stream.Read(ctx, buf); err != nil {
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	cid := utp.NewConnectionId(b.Addr(), 9400, 9401)
	stream, err := sockA.ConnectWithCid(ctx, cid, sendCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()

	bulk := make([]byte, 512*1024)
	rand.New(rand.NewSource(11)).Read(bulk)
	bulkStart := time.Now()
	if _, err := stream.Write(ctx, bulk); err != nil {
		t.Fatalf("bulk write: %v", err)
	}
	quietFrom := waitForIdle(t, &mu, &samples, bulkStart, 60*time.Second)

	mu.Lock()
	timeoutsAfterBulk := lastSample(samples).Timeouts
	cwndAfterBulk := lastSample(samples).CwndBytes
	mu.Unlock()

	// Now the quiet phase: one small write every 200ms, each one comfortably
	// acknowledged long before any plausible timeout.
	const quietPhase = 5 * time.Second
	trickle := make([]byte, 200)
	deadline := time.Now().Add(quietPhase)
	for time.Now().Before(deadline) {
		if _, err := stream.Write(ctx, trickle); err != nil {
			t.Fatalf("trickle write: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	mu.Lock()
	got := append([]utp.ConnectionMetrics(nil), samples...)
	mu.Unlock()

	final := lastSample(got)
	timedOut := final.Timeouts - timeoutsAfterBulk
	retxDuringQuiet := final.PacketsRetransmitted - retxAt(got, quietFrom)

	if timedOut > 0 {
		t.Errorf("a connection with nothing lost timed out %d time(s) over %v of small writes, "+
			"taking the congestion window from %d to %d bytes and retransmitting %d packet(s) "+
			"that had already arrived; libutp only treats the deadline as a loss when something "+
			"is outstanding (utp_internal.cpp:1147-1155)",
			timedOut, quietPhase, cwndAfterBulk, final.CwndBytes, retxDuringQuiet)
	}
	t.Logf("%v quiet: %d timeouts, %d retransmissions, window %d -> %d bytes",
		quietPhase, timedOut, retxDuringQuiet, cwndAfterBulk, final.CwndBytes)
}

func debugIfSet() elog.Logger {
	if os.Getenv("UTP_IDLE_DEBUG") == "" {
		return quiet()
	}
	return elog.NewLogger(elog.NewTerminalHandlerWithLevel(os.Stderr, elog.LevelDebug, false))
}

func lastSample(samples []utp.ConnectionMetrics) utp.ConnectionMetrics {
	if len(samples) == 0 {
		return utp.ConnectionMetrics{}
	}
	return samples[len(samples)-1]
}

func retxAt(samples []utp.ConnectionMetrics, at time.Time) uint64 {
	var n uint64
	for _, m := range samples {
		if m.At.After(at) {
			break
		}
		n = m.PacketsRetransmitted
	}
	return n
}
