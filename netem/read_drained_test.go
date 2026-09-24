package netem

import (
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// utp_read_drained, end to end (utp_internal.cpp:3242-3261).
//
// A reader pauses long enough for the sender to fill its 32KB receive buffer.
// The window falls below a packet but not to zero, so the zero-window probe
// never arms, and nothing is in flight, so no retransmission timer runs. When
// the reader drains, only this end speaking can restart the sender.
//
// Measured, 10 runs an arm: 2.158-2.163s with the mechanism; 29.698-29.702s
// without it, with the keep-alive checked every 500ms as libutp checks it,
// the keep-alive being the only thing that recovered the control. See KNOWN-LIMITATIONS.md, M4b sweep,
// section 2. The budget below sits between them with room on both sides.
func TestReadDrainedRestartsASenderBlockedBySubPacketWindow(t *testing.T) {
	const readerPause = 2 * time.Second
	const budget = 10 * time.Second
	const payloadSize = 512 * 1024

	n := NewNetwork(77)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	// A queue far larger than anything in flight: the link drops nothing, so
	// the only way to close the window is the paused reader.
	n.Connect(a, b, Config{Delay: 5 * time.Millisecond, BandwidthBps: 50_000_000, QueueBytes: 8 << 20})

	ctx, cancel := context.WithTimeout(context.Background(), 2*budget)
	defer cancel()
	sendSock := utp.WithSocket(ctx, a, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	const cid = 900
	acceptCid := utp.NewConnectionId(a.Addr(), cid+1, cid)
	connectCid := utp.NewConnectionId(b.Addr(), cid, cid+1)
	payload := make([]byte, payloadSize)

	var mu sync.Mutex
	var (
		minPeerWindow = uint32(1 << 31)
		mtu           uint32
		drops         uint64
		reopenAcks    uint64
	)
	recvCfg := utp.NewConnectionConfig()
	recvCfg.BufferSize = 32 * 1024
	recvCfg.MetricsInterval = 10 * time.Millisecond
	recvCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		drops = m.RecvBufferDrops
		reopenAcks = m.WindowReopenedAcks
	}
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 10 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		if m.PeerRecvWindow < minPeerWindow {
			minPeerWindow = m.PeerRecvWindow
		}
		mtu = m.MtuCurrent
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	var got int
	var elapsed time.Duration
	go func() {
		defer wg.Done()
		rctx, rcancel := context.WithTimeout(ctx, budget)
		defer rcancel()
		stream, err := recvSock.AcceptWithCid(rctx, acceptCid, recvCfg)
		if err != nil {
			return
		}
		defer stream.Close()
		time.Sleep(readerPause)
		buf := make([]byte, 0, payloadSize)
		_, _ = stream.ReadToEOF(rctx, &buf)
		got = len(buf)
		elapsed = time.Since(start)
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			return
		}
		defer stream.Close()
		_, _ = stream.Write(ctx, payload)
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	t.Logf("delivered %d of %d in %v; peer window fell to %d (packet %d); "+
		"%d reopening acks, %d receive-buffer drops",
		got, payloadSize, elapsed, minPeerWindow, mtu, reopenAcks, drops)

	// The scenario, before the outcome: otherwise a pass could mean the
	// window never closed, or closed to zero and the probe did the work.
	if minPeerWindow == 0 {
		t.Fatalf("the window closed to zero; the zero-window probe covers that " +
			"case and this one would not isolate the mechanism")
	}
	if minPeerWindow >= mtu {
		t.Fatalf("the window never fell below a packet (%d against %d); the "+
			"sender was never blocked and this case asserts nothing", minPeerWindow, mtu)
	}
	if drops != 0 {
		t.Fatalf("%d packets refused by the receive buffer on a link that drops "+
			"nothing: the sender overran the window, and the recovery would be "+
			"retransmission rather than this mechanism", drops)
	}

	if got != payloadSize {
		t.Fatalf("delivered %d of %d within %v: the drain did not reopen the "+
			"sender (without utp_read_drained this takes ~29.3s, one keep-alive interval)", got, payloadSize, budget)
	}
	if reopenAcks == 0 {
		t.Errorf("delivered, but no acknowledgement was counted as owed to the drain")
	}
}
