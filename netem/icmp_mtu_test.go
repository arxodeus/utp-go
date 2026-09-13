package netem

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// icmpTransferResult is what one run of icmpTransfer observed.
type icmpTransferResult struct {
	floor, current, ceiling uint32
	delivered               int
	correct                 bool
	readErr, writeErr       error
	elapsed                 time.Duration
	fwd                     Stats
	reports                 uint64
	matched                 uint64
}

// icmpTransfer runs one transfer over a size-limited path, optionally with the
// router reporting each refusal the way a real one does.
//
// quoteBytes is how much of the offending datagram the router quotes back. A
// real router is only required to quote 28 bytes past the IP header, of which
// 8 are the UDP header, so 20 bytes of uTP header is the least a receiver of
// ICMP can count on.
func icmpTransfer(t *testing.T, linkMTU int, payloadLen int, cid uint16, withICMP bool, quoteBytes int) icmpTransferResult {
	t.Helper()

	var res icmpTransferResult
	var sendSock atomic.Pointer[utp.UtpSocket]
	var reports, matched atomic.Uint64

	cfg := Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
		MTU:          linkMTU,
	}

	n := NewNetwork(51)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")

	if withICMP {
		cfg.OnMTUDrop = func(payload []byte, src, dst string, quotedLinkMTU int) {
			// Only the sender's own datagrams concern the sender's socket.
			if src != "sender" {
				return
			}
			sock := sendSock.Load()
			if sock == nil {
				return
			}
			reports.Add(1)
			quoted := payload
			if quoteBytes > 0 && len(quoted) > quoteBytes {
				quoted = quoted[:quoteBytes]
			}
			if sock.ProcessICMPFragmentation(quoted, b.Addr(), uint16(quotedLinkMTU)) {
				matched.Add(1)
			}
		}
	}
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sendSocket := utp.WithSocket(ctx, a, quiet())
	defer sendSocket.Close()
	sendSock.Store(sendSocket)
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(a.Addr(), cid+1, cid)
	connectCid := utp.NewConnectionId(b.Addr(), cid, cid+1)

	var mu sync.Mutex
	var samples int
	// A path this narrow stalls the transfer (see the comment on
	// TestIcmpBringsTheSearchWithinThePath), so both ends give up early
	// rather than sitting out the default minute. The search has settled
	// long before then; what is measured is where it settled.
	const idleTimeout = 8 * time.Second
	recvCfg := utp.NewConnectionConfig()
	recvCfg.MaxIdleTimeout = idleTimeout
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MaxIdleTimeout = idleTimeout
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		defer mu.Unlock()
		res.floor, res.current, res.ceiling = m.MtuFloor, m.MtuCurrent, m.MtuCeiling
		samples++
	}

	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, recvCfg)
		if err != nil {
			mu.Lock()
			res.readErr = err
			mu.Unlock()
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, payloadLen)
		_, err = stream.ReadToEOF(ctx, &buf)
		mu.Lock()
		res.readErr = err
		res.delivered = len(buf)
		res.correct = bytes.Equal(buf, payload)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSocket.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			mu.Lock()
			res.writeErr = err
			mu.Unlock()
			return
		}
		defer stream.Close()
		_, err = stream.Write(ctx, payload)
		mu.Lock()
		res.writeErr = err
		mu.Unlock()
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	res.elapsed = time.Since(start)
	res.fwd = n.Link("sender", "receiver").Stats()
	res.reports = reports.Load()
	res.matched = matched.Load()
	if samples == 0 {
		t.Fatal("no metric samples recorded; the test observed nothing")
	}
	return res
}

// What the router's report is worth on a path narrower than the search's own
// ceiling: the search ends up at a size the path carries, instead of parked
// above it sending datagrams that can never arrive.
//
// The control is the same transfer with the router silent, which is the
// behaviour TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice records as
// a known limitation: the ceiling only comes down when a probe times out as
// the *sole* outstanding packet (utp_internal.cpp:1152-1160), and a
// connection whose every packet is too big never gets back to one outstanding
// packet. libutp is measured doing the same thing in
// TestLibutpStallsOnAPathItCannotFit.
//
// **This does not rescue the transfer, and the test says so rather than
// pretending otherwise.** uTP numbers packets, not bytes, so a packet that has
// already been built cannot be re-cut smaller -- doing that would renumber
// every packet behind it. libutp has the same constraint and gets away with it
// by not setting don't-fragment on ordinary data ("now we need it to fragment
// just to get it through", utp_internal.cpp:898-905), leaving the router to
// fragment what it cannot forward whole. This emulated link refuses oversized
// datagrams outright, which is what an IPv6 path does and what Linux's default
// IP_PMTUDISC_WANT makes an IPv4 path do, so the packets already in the window
// stay stuck under either implementation.
//
// So what ICMP fixes here is the search, not the window: every packet built
// from the report onwards fits the path. That is what this asserts.
func TestIcmpBringsTheSearchWithinThePath(t *testing.T) {
	const linkMTU = 1100
	const payloadLen = 1 << 20

	silent := icmpTransfer(t, linkMTU, payloadLen, 720, false, 0)
	t.Logf("router silent:   delivered %d/%d in %v; search floor=%d current=%d ceiling=%d; link %s",
		silent.delivered, payloadLen, silent.elapsed,
		silent.floor, silent.current, silent.ceiling, silent.fwd)

	informed := icmpTransfer(t, linkMTU, payloadLen, 730, true, 0)
	t.Logf("router reports:  delivered %d/%d in %v; search floor=%d current=%d ceiling=%d; link %s; %d reports, %d matched",
		informed.delivered, payloadLen, informed.elapsed,
		informed.floor, informed.current, informed.ceiling, informed.fwd,
		informed.reports, informed.matched)

	// The control has to park above the path, or there is nothing to fix and
	// the comparison proves nothing.
	if silent.current <= uint32(linkMTU) {
		t.Fatalf("with the router silent the search settled on %d bytes, already within the "+
			"%d-byte link; this path does not exercise what the report is for",
			silent.current, linkMTU)
	}

	if informed.reports == 0 {
		t.Fatal("the router never refused a datagram, so no report was ever sent")
	}
	if informed.matched != informed.reports {
		t.Errorf("%d of %d reports matched a connection; every one quotes a datagram this "+
			"socket sent", informed.matched, informed.reports)
	}
	if informed.current > uint32(linkMTU) {
		t.Errorf("with the router reporting, the search still builds %d-byte datagrams for a "+
			"%d-byte link", informed.current, linkMTU)
	}
	if informed.ceiling > uint32(linkMTU) {
		t.Errorf("with the router reporting, the ceiling is still %d against a %d-byte link, "+
			"so the search would climb back above the path", informed.ceiling, linkMTU)
	}

	// Recorded, not asserted: the transfer still stalls, for the reason in the
	// comment above. Asserting the stall would fix it in place; leaving it
	// unmeasured would let it quietly become something else.
	t.Logf("residual: silent delivered %d bytes, informed delivered %d of %d -- "+
		"packets built before the report keep their size and uTP cannot re-cut them",
		silent.delivered, informed.delivered, payloadLen)
}

// A router that quotes only the 20 bytes it has to must still be understood.
// Quoting more is common and quoting less is legal, so the parse cannot depend
// on the extensions or the payload being there.
func TestIcmpWorksFromAMinimalQuote(t *testing.T) {
	const linkMTU = 1100
	const payloadLen = 1 << 20

	// 20 bytes: the uTP header and nothing else.
	res := icmpTransfer(t, linkMTU, payloadLen, 740, true, 20)
	t.Logf("20-byte quote: delivered %d/%d in %v; search floor=%d current=%d ceiling=%d; %d reports, %d matched",
		res.delivered, payloadLen, res.elapsed, res.floor, res.current, res.ceiling,
		res.reports, res.matched)

	if res.reports == 0 {
		t.Fatal("the router never refused a datagram, so no report was ever sent")
	}
	if res.matched != res.reports {
		t.Errorf("%d of %d minimal quotes matched a connection; a 20-byte quote carries "+
			"everything the lookup needs", res.matched, res.reports)
	}
	if res.current > uint32(linkMTU) || res.ceiling > uint32(linkMTU) {
		t.Errorf("the search settled at current=%d ceiling=%d against a %d-byte link",
			res.current, res.ceiling, linkMTU)
	}
}

// On a path that carries everything the search will ever ask for, no report is
// generated and nothing changes. Stated so that "ICMP helps" cannot quietly
// become "ICMP is required".
func TestIcmpChangesNothingOnAnUnconstrainedPath(t *testing.T) {
	const payloadLen = 1 << 20
	res := icmpTransfer(t, 1500, payloadLen, 750, true, 0)
	t.Logf("link MTU 1500: delivered %d/%d; search floor=%d current=%d ceiling=%d; %d reports",
		res.delivered, payloadLen, res.floor, res.current, res.ceiling, res.reports)

	if res.reports != 0 {
		t.Errorf("%d datagrams were refused for size on a 1500-byte path", res.reports)
	}
	if res.delivered != payloadLen || !res.correct {
		t.Errorf("delivered %d of %d bytes, correct=%v", res.delivered, payloadLen, res.correct)
	}
}
