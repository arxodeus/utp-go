package netem

import (
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// mtuTransfer runs one transfer over a link with the given config and returns
// the sender's last observed MTU search state, plus the link's stats.
func mtuTransfer(t *testing.T, cfg Config, payloadLen int, cid uint16) (floor, current, ceiling uint32, fwd Stats) {
	t.Helper()

	n := NewNetwork(51)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sendSock := utp.WithSocket(ctx, a, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(a.Addr(), cid+1, cid)
	connectCid := utp.NewConnectionId(b.Addr(), cid, cid+1)

	var (
		mu      sync.Mutex
		samples int
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		floor, current, ceiling = m.MtuFloor, m.MtuCurrent, m.MtuCeiling
		samples++
	}

	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, payloadLen)
		if _, err := stream.ReadToEOF(ctx, &buf); err != nil {
			t.Errorf("read: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			t.Errorf("connect: %v", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if samples == 0 {
		t.Fatal("no metric samples recorded; the test observed nothing")
	}
	return floor, current, ceiling, n.Link("sender", "receiver").Stats()
}

// Path-MTU discovery, against a path that actually limits size.
//
// Until the link model grew an MTU, every emulated path carried any datagram
// however large, so the search always converged on whatever ceiling it was
// given and the half of it that *lowers* the ceiling was never exercised end
// to end.
//
// This is the case that works: the path carries more than the search will ever
// ask for, so the search climbs to its own ceiling and settles.
func TestMtuSearchConvergesWhenThePathAllowsIt(t *testing.T) {
	floor, current, ceiling, fwd := mtuTransfer(t, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
		MTU:          1500,
	}, 1<<20, 700)

	t.Logf("link MTU 1500: search settled at floor=%d current=%d ceiling=%d; link %s",
		floor, current, ceiling, fwd)

	if fwd.DroppedByMTU != 0 {
		t.Errorf("%d datagrams were refused for size on a path that carries 1500 bytes; "+
			"the search sent something it should never have built", fwd.DroppedByMTU)
	}
	if current < 1300 {
		t.Errorf("the search settled on %d-byte datagrams on a 1500-byte path; it should "+
			"reach its own 1400-byte ceiling", current)
	}
	if !(floor == current && current == ceiling) {
		t.Errorf("the search did not converge: floor=%d current=%d ceiling=%d", floor, current, ceiling)
	}
}

// A path whose MTU is below the size the search has already adopted.
//
// SKIPPED: this is a real limitation, and it is libutp's as much as ours.
//
// The search raises the size it sends at whenever a probe is acknowledged. If
// the path limit sits between two probe sizes, the next size it adopts cannot
// arrive -- and because every data packet is then built at that size, nothing
// arrives at all. The ceiling only comes down when a probe times out as the
// *only* packet outstanding (utp_internal.cpp:1152-1160), which a connection
// with a stalled window never reaches: nothing is acknowledged, so the
// outstanding count never falls to one.
//
// Measured here, on a path that refuses anything over 1100 bytes:
//
//	ours    search parks at 1191 bytes; a 1MB transfer delivers ~5KB and the
//	        connection gives up
//	libutp  sends ~1230-byte packets, delivers 20 bytes, and reports a
//	        connection error (TestLibutpStallsOnAPathItCannotFit)
//
// So this is not a difference from the reference to be closed. Fixing it means
// deliberately diverging -- concluding "too big" from repeated timeouts with no
// progress, rather than only from a solitary probe -- and that belongs in
// DEVIATIONS.md with a measurement behind it, not in a quiet patch.
//
// It matters in practice: a path MTU below 1400 is ordinary (PPPoE at 1492,
// most VPN and tunnel paths), and a BitTorrent client on one would stall.
func TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice(t *testing.T) {
	t.Skip("known limitation, shared with libutp: the search cannot lower its ceiling " +
		"once the size it has adopted stalls the connection; see KNOWN-LIMITATIONS.md")

	const linkMTU = 1100
	floor, current, ceiling, fwd := mtuTransfer(t, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
		MTU:          linkMTU,
	}, 1<<20, 710)

	t.Logf("link MTU %d: search settled at floor=%d current=%d ceiling=%d; link %s",
		linkMTU, floor, current, ceiling, fwd)

	if current > linkMTU {
		t.Errorf("the search settled on %d-byte datagrams over a path that drops anything "+
			"above %d; those packets cannot arrive", current, linkMTU)
	}
}
