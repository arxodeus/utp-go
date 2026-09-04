package netem

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

func quiet() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// The harness has to actually carry uTP, and the connection-level metrics
// have to record something. Without this, the M5 gates would be measuring an
// emulator nobody had run a real flow over.

func TestUtpTransferOverEmulatedLink(t *testing.T) {
	n := NewNetwork(21)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, 512*1024)
	for i := range data {
		data[i] = byte(i * 31)
	}

	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    100,
		MetricsInterval: 2 * time.Millisecond,
		Verify:          true,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	t.Logf("transfer: %s", res)
	t.Logf("sender:   %s", res.Sender.Summary())
	t.Logf("link a->b: %s", n.Link("sender", "receiver").Stats())

	if !res.Verified {
		t.Fatalf("payload mismatch: got %d bytes, want %d", res.BytesTransferred, len(data))
	}
	if res.Sender.Len() == 0 {
		t.Fatal("no metric samples recorded on the sender")
	}
}

// The window has to move. A congestion controller whose window never changes
// is not controlling anything -- which is exactly the failure this fork
// already found once, and the reason the harness records cwnd at all.
func TestCongestionWindowRespondsOverEmulatedLink(t *testing.T) {
	n := NewNetwork(22)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 8_000_000,
		QueueBytes:   48 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, 768*1024)
	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    200,
		MetricsInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	sum := res.Sender.Summary()
	t.Logf("transfer: %s", res)
	t.Logf("sender:   %s", sum)

	if sum.CwndMaxBytes == 0 {
		t.Fatal("congestion window was never observed above zero")
	}
	if sum.CwndMaxBytes == sum.CwndMinBytes {
		t.Errorf("congestion window never moved: constant at %d bytes -- the controller is not responding to anything",
			sum.CwndMaxBytes)
	}
	if sum.RTTP50 == 0 {
		t.Error("no RTT estimate was ever recorded")
	}
	// The link has 20ms one-way delay each way, so a sane RTT estimate must
	// be at least that. This is what catches an RTT estimator wired to the
	// wrong clock.
	if sum.RTTP50 < 30*time.Millisecond {
		t.Errorf("median RTT %v is implausibly low for a 40ms round trip", sum.RTTP50)
	}
}

// Loss must be survivable, and must show up as retransmissions.
func TestUtpSurvivesLossOverEmulatedLink(t *testing.T) {
	n := NewNetwork(23)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	cfg := Config{
		Delay:        10 * time.Millisecond,
		LossRate:     0.02,
		BandwidthBps: 10_000_000,
		QueueBytes:   64 * 1024,
	}
	n.ConnectAsymmetric(a, b, cfg, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, 256*1024)
	for i := range data {
		data[i] = byte(i)
	}

	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    300,
		MetricsInterval: 2 * time.Millisecond,
		Verify:          true,
	})
	if err != nil {
		t.Fatalf("transfer over a 2%% loss link: %v", err)
	}
	sum := res.Sender.Summary()
	t.Logf("transfer: %s", res)
	t.Logf("sender:   %s", sum)
	t.Logf("link a->b: %s", n.Link("sender", "receiver").Stats())

	if !res.Verified {
		t.Fatalf("payload corrupted across a lossy link: got %d bytes, want %d", res.BytesTransferred, len(data))
	}
	if sum.PacketsRetransmitted == 0 {
		t.Error("2% loss produced no retransmissions; either loss is not reaching uTP or retransmits are not counted")
	}
}

// Two flows over one bottleneck, and the capacity split reported. This is the
// mechanism the M5 fairness gate needs; here it only has to work, not to be
// fair -- judging the split is M5's job.
func TestTwoUtpFlowsShareBottleneck(t *testing.T) {
	n := NewNetwork(24)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        15 * time.Millisecond,
		BandwidthBps: 8_000_000,
		QueueBytes:   64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, 384*1024)
	type out struct {
		res *TransferResult
		err error
	}
	results := make(chan out, 2)
	for i, cid := range []uint16{400, 500} {
		go func(i int, cid uint16) {
			r, err := pair.RunTransfer(ctx, data, FlowOptions{
				InitiatorCid:    cid,
				MetricsInterval: 5 * time.Millisecond,
			})
			results <- out{r, err}
		}(i, cid)
	}

	rates := make([]float64, 0, 2)
	for i := 0; i < 2; i++ {
		o := <-results
		if o.err != nil {
			t.Fatalf("flow %d: %v", i, o.err)
		}
		if !o.res.Verified {
			t.Errorf("flow %d transferred %d of %d bytes", i, o.res.BytesTransferred, len(data))
		}
		t.Logf("flow %d: %s", i, o.res)
		t.Logf("flow %d: %s", i, o.res.Sender.Summary())
		rates = append(rates, o.res.Goodput.Bps())
	}

	fairness := FairnessIndex(rates)
	total := (rates[0] + rates[1]) / 1e6
	t.Logf("two uTP flows over an 8 Mbps bottleneck: %.2f + %.2f = %.2f Mbps, Jain fairness %.3f",
		rates[0]/1e6, rates[1]/1e6, total, fairness)
	t.Logf("link a->b: %s", n.Link("sender", "receiver").Stats())

	// The harness gate is only that both flows complete and the split is
	// reportable. Whether the split is *fair* is an M5 question about the
	// congestion controller, not about this emulator.
	if rates[0] == 0 || rates[1] == 0 {
		t.Errorf("a flow achieved zero goodput: %v", rates)
	}
}

// Conditions can change mid-flight, which soak and recovery tests need.
func TestLinkConfigCanChangeMidFlight(t *testing.T) {
	n := NewNetwork(25)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: 5 * time.Millisecond})

	buf := make([]byte, 65535)
	sent := time.Now()
	if _, err := a.WriteTo(makePayload(1, 64), b.Addr()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
	before := time.Since(sent)

	if err := n.SetConfig("a", "b", Config{Delay: 60 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}

	sent = time.Now()
	if _, err := a.WriteTo(makePayload(2, 64), b.Addr()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
	after := time.Since(sent)

	t.Logf("delay before %v, after raising to 60ms: %v", before.Round(time.Millisecond), after.Round(time.Millisecond))
	if after < 55*time.Millisecond {
		t.Errorf("raising the delay to 60ms produced %v", after)
	}
	if before > 20*time.Millisecond {
		t.Errorf("initial 5ms delay measured %v", before)
	}
}

// TestSpuriousRetransmitsOnLosslessLink measures retransmissions on a link
// that drops nothing. The correct answer is zero: with no loss and no
// reordering, nothing should ever need resending.
//
// It was not zero. On an 8 Mbps / 40 ms path this implementation retransmitted
// about 6% of packets, because each connection owned a retransmission timer
// wheel with a one-second resolution (interval = InitialTimeout/4) which
// cannot represent the 500 ms RTO -- a timer set for "500 ms" landed on the
// next tick, uniformly 0-1000 ms away, so packets were declared lost long
// before their ack could arrive.
//
// The wheel is now shared across the socket at 25 ms resolution, which buys
// the accuracy without a ticker per connection. Measured: 0.00%.
func TestSpuriousRetransmitsOnLosslessLink(t *testing.T) {
	n := NewNetwork(26)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 8_000_000,
		QueueBytes:   48 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, 768*1024)
	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    600,
		MetricsInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	sum := res.Sender.Summary()
	linkStats := n.Link("sender", "receiver").Stats()

	t.Logf("transfer: %s", res)
	t.Logf("sender:   %s", sum)
	t.Logf("link:     %s", linkStats)
	t.Logf("SPURIOUS RETRANSMIT RATE on a lossless link: %.2f%% (target 0%%)", sum.RetransmitRate*100)

	if linkStats.PacketsDropped != 0 {
		t.Fatalf("this link is meant to be lossless but dropped %d packets; the measurement is invalid",
			linkStats.PacketsDropped)
	}
	// A lossless link should need no retransmissions at all. The small
	// allowance covers a genuinely late ack under scheduler noise, not the
	// systematic mistiming this test was written to catch.
	if sum.RetransmitRate > 0.01 {
		t.Errorf("retransmit rate %.2f%% on a lossless link, want ~0%%", sum.RetransmitRate*100)
	}
}

// TestConnectionGivesUpWhenPeerGoesSilent covers libutp's give-up rule: a
// connection dies after a few consecutive retransmission timeouts with
// nothing acked, rather than lingering until the idle timer.
//
// Without it a connection has no death condition of its own. The idle timer
// is reset by *any* inbound packet, so a half-broken peer that keeps sending
// anything at all holds the connection open indefinitely while data goes
// unacked -- and for a BitTorrent client with many peers, that is a lot of
// dead connections held open.
func TestConnectionGivesUpWhenPeerGoesSilent(t *testing.T) {
	n := NewNetwork(27)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	linkCfg := Config{Delay: 10 * time.Millisecond}
	n.ConnectAsymmetric(a, b, linkCfg, linkCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	cfg := utp.NewConnectionConfig()
	// Shorten the RTO so the four retransmissions do not take ten seconds.
	// The idle timeout stays at its 60s default: the point is that the
	// give-up rule fires long before it.
	cfg.InitialTimeout = 200 * time.Millisecond
	cfg.MinTimeout = 100 * time.Millisecond
	idleTimeout := cfg.MaxIdleTimeout

	accCid := utp.NewConnectionId(a.Addr(), 801, 800)
	iniCid := utp.NewConnectionId(b.Addr(), 800, 801)

	accepted := make(chan error, 1)
	go func() {
		s, err := pair.SockB.AcceptWithCid(ctx, accCid, cfg)
		if err != nil {
			accepted <- err
			return
		}
		accepted <- nil
		buf := make([]byte, 0)
		_, _ = s.ReadToEOF(ctx, &buf)
	}()

	stream, err := pair.SockA.ConnectWithCid(ctx, iniCid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Blackhole the path in both directions: the peer is now unreachable but
	// the connection is fully established.
	dead := Config{LossRate: 1.0, Delay: 10 * time.Millisecond}
	if err := n.SetConfig("sender", "receiver", dead); err != nil {
		t.Fatal(err)
	}
	if err := n.SetConfig("receiver", "sender", dead); err != nil {
		t.Fatal(err)
	}

	// Write more than the send buffer holds, so the call blocks waiting for
	// window rather than returning as soon as the buffer accepts it. That is
	// the case a caller actually notices: a write that can never complete
	// must fail, not hang until the idle timer.
	payload := make([]byte, 4*int(cfg.BufferSize))
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := stream.Write(ctx, payload)
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		t.Logf("write returned after %v with err=%v (idle timeout is %v)",
			elapsed.Round(time.Millisecond), err, idleTimeout)
		if err == nil {
			t.Error("write to an unreachable peer reported success")
		}
		if elapsed >= idleTimeout {
			t.Errorf("took %v to give up, which is the idle timeout (%v) rather than the retransmission limit",
				elapsed, idleTimeout)
		}
		// Four retransmissions with the configured backoff is a couple of
		// seconds at most; anything near the idle timeout means the give-up
		// rule is not what ended it.
		if elapsed > 30*time.Second {
			t.Errorf("took %v to give up, far longer than the retransmission limit implies", elapsed)
		}
	case <-time.After(idleTimeout):
		t.Fatalf("connection had not given up after %v; it is relying on the idle timer, not the retransmission limit", idleTimeout)
	}
}
