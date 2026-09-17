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
// STILL SKIPPED, but for a narrower reason than it was.
//
// The search raises the size it sends at whenever a probe is acknowledged. If
// the path limit sits between two probe sizes, the next size it adopts cannot
// arrive -- and because every data packet is then built at that size, nothing
// arrives at all.
//
// This used to say the search could never come back, because the ceiling only
// falls when a probe times out as the *only* packet outstanding
// (utp_internal.cpp:1152-1160), which a stalled connection never reaches. That
// was true of this library and is no longer: libutp's other route, three
// duplicate acknowledgements pointing at the packet before the probe
// (:1927-1940), is implemented now, and it works while the window is full.
// Measured on this same 1100-byte link, with the skip lifted:
//
//	before  search parks at floor=576 current=1191 ceiling=1400 -- above the path
//	after   search comes down to floor=982 current=1083 ceiling=1184
//
// So the search does recover, and the sizes it builds from then on fit. What
// does not recover is the transfer: it still delivers a few kilobytes of a
// megabyte and the reader times out after 60 seconds, which is why this stays
// skipped. uTP numbers packets, not bytes, so the packets already built at
// 1191 bytes cannot be re-cut smaller -- that would renumber everything behind
// them -- and the peer will never acknowledge packets it cannot receive.
// libutp has the same constraint and gets away with it by not setting
// don't-fragment on ordinary data ("now we need it to fragment just to get it
// through", :898-905), leaving the router to fragment what it cannot forward
// whole. This emulated link refuses oversized datagrams outright, which is
// what an IPv6 path does.
//
// The remaining ceiling matters too: 1184 is still above the 1100-byte path,
// so the search could climb back. An ICMP report brings it to exactly the
// path -- see TestIcmpBringsTheSearchWithinThePath, where that is now the
// difference the report makes.
//
// It matters in practice: a path MTU below 1400 is ordinary (PPPoE at 1492,
// most VPN and tunnel paths), and a BitTorrent client on one would stall a
// connection it had already put packets on.
func TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice(t *testing.T) {
	t.Skip("the search now recovers -- the duplicate-acknowledgement route lowers the " +
		"ceiling while the window is full -- but the transfer does not: packets already " +
		"built too big cannot be re-cut, so the reader still times out after 60s and this " +
		"harness fails on that. Prevented rather than recovered from where the narrow link " +
		"is the local interface: TestPathMTUReportPreventsTheStall runs this same link " +
		"with the sender told what its interface carries, and the transfer completes")

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

// The stall above, prevented rather than recovered from.
//
// TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice is skipped because
// once the search has adopted a size the path will not carry, neither this
// library nor libutp can get back: every packet is too big, nothing is
// acknowledged, and the ceiling only comes down when a probe times out as the
// sole outstanding packet.
//
// The way out is to never adopt that size, which is what libutp's
// UTP_GET_UDP_MTU callback is for -- it sets the ceiling from the local
// interface before the first packet goes out. This library had no equivalent
// until utp_go.PathMTUProvider; here the endpoint answers it, as a host
// behind a tunnel would.
func TestPathMTUReportPreventsTheStall(t *testing.T) {
	const linkMTU = 1100
	const payloadLen = 1 << 20

	n := NewNetwork(52)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
		MTU:          linkMTU,
	})
	// The sender knows what its own interface carries. The receiver is not
	// told, and does not need to be: it sends only acknowledgements.
	a.ReportPathMTU(linkMTU)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sendSock := utp.WithSocket(ctx, a, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(a.Addr(), 761, 760)
	connectCid := utp.NewConnectionId(b.Addr(), 760, 761)

	var (
		mu      sync.Mutex
		current uint32
		ceiling uint32
		samples int
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		current, ceiling = m.MtuCurrent, m.MtuCeiling
		samples++
	}

	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	var delivered int
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
		mu.Lock()
		delivered = len(buf)
		mu.Unlock()
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

	fwd := n.Link("sender", "receiver").Stats()
	mu.Lock()
	defer mu.Unlock()
	if samples == 0 {
		t.Fatal("no metric samples recorded; the test observed nothing")
	}
	t.Logf("link MTU %d, reported to the sender: delivered %d/%d; search current=%d ceiling=%d; link %s",
		linkMTU, delivered, payloadLen, current, ceiling, fwd)

	if fwd.DroppedByMTU != 0 {
		t.Errorf("%d datagrams were refused for size on a path whose limit the sender "+
			"was told; it should never have built one", fwd.DroppedByMTU)
	}
	if delivered != payloadLen {
		t.Errorf("delivered %d of %d bytes", delivered, payloadLen)
	}
	if ceiling > linkMTU {
		t.Errorf("ceiling %d against a %d-byte link", ceiling, linkMTU)
	}
}
