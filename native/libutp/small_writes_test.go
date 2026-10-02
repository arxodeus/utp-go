//go:build cgo

package libutp_test

import (
	"context"
	"sort"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// spinUntil waits for t without sleeping past it: Go timers wake about 1.1 ms
// late on an idle runtime.
func spinUntil(t time.Time) {
	if d := time.Until(t); d > 2*time.Millisecond {
		time.Sleep(d - 1500*time.Microsecond)
	}
	for time.Now().Before(t) {
	}
}

type smallResult struct {
	elapsed       time.Duration
	dataPackets   int
	p50, p99, max time.Duration
}

// smallWrites sends count messages of size bytes, one every interval, from
// libutp or from this library, to this library's receiver, and reports what
// went over the data path and how long each message took to arrive.
func smallWrites(t *testing.T, libutpSends bool, path pathConfig, count, size int, interval time.Duration) smallResult {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	recv, recvPort := bindGoSocket(t, ctx)
	defer recv.Close()

	total := count * size
	sentAt := make([]time.Time, count)
	gotAt := make([]time.Time, count)
	done := make(chan error, 1)
	go func() {
		s, err := recv.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			done <- err
			return
		}
		defer s.Close()
		buf := make([]byte, 64*1024)
		n := 0
		for n < total {
			k, err := s.Read(ctx, buf)
			now := time.Now()
			for i := n / size; i < (n+k)/size; i++ {
				gotAt[i] = now
			}
			n += k
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	var dataEv []relayEvent
	var senderPort uint16
	var peer *libutp.Peer
	var sock *utp.UtpSocket
	if libutpSends {
		p, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Shutdown()
		peer, senderPort = p, p.Port()
	} else {
		s, port := bindGoSocket(t, ctx)
		defer s.Close()
		sock, senderPort = s, port
	}
	onNewRelay = func(r *relay) { r.toGo.delivered = &dataEv }
	r := newRelay(t, senderPort, recvPort, path, path, 1)
	onNewRelay = nil
	defer r.Close()

	msg := make([]byte, size)
	var write func([]byte)
	if libutpSends {
		if err := peer.Connect(r.LibutpFacingPort()); err != nil {
			t.Fatal(err)
		}
		if _, err := peer.WaitState(30*time.Second, libutp.StateConnected); err != nil {
			t.Fatal(err)
		}
		write = func(b []byte) { _, _ = peer.Write(b) }
	} else {
		cid := utp.NewConnectionId(utp.NewUdpPeer(loopback(r.LibutpFacingPort())), 5100, 5101)
		// The bridge tells libutp its path carries 1472-byte datagrams
		// (bridge.cpp, cb_get_udp_mtu); the same ceiling here, so that the
		// packets compared are the same size.
		cfg := utp.NewConnectionConfig()
		cfg.MaxPacketSize = 1472
		stream, err := sock.ConnectWithCid(ctx, cid, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		write = func(b []byte) { _, _ = stream.Write(ctx, b) }
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	for i := 0; i < count; i++ {
		spinUntil(start.Add(time.Duration(i) * interval))
		for j := range msg {
			msg[j] = byte(i + j)
		}
		sentAt[i] = time.Now()
		write(msg)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("not delivered")
	}
	// dataEv is the relay's to append to until it has stopped.
	r.Close()
	var lat []time.Duration
	for i := range sentAt {
		lat = append(lat, gotAt[i].Sub(sentAt[i]))
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	res := smallResult{elapsed: gotAt[count-1].Sub(start), p50: lat[len(lat)/2], p99: lat[len(lat)*99/100], max: lat[len(lat)-1]}
	for _, e := range dataEv {
		if e.Type == 0 {
			res.dataPackets++
		}
	}
	return res
}

// TestSmallWritesAgainstLibutp writes 2,000 messages of 100 bytes, one every
// 0.5 ms, over a 20 ms round trip -- a peer sending protocol messages faster
// than they can be acknowledged -- from libutp and then from this library,
// to this library's receiver, and counts the data packets each put on the
// path.
//
// libutp coalesces them: a short packet waits while anything is
// unacknowledged and later writes join it (flush_packets,
// utp_internal.cpp:974-982; write_outgoing_packet, :1013-1023). Measured, with
// both at the 1472-byte datagram the bridge reports to libutp: 139 packets
// from each. The gate is our count within smallWritesSlack of libutp's.
//
// With NoDelay, or without the Nagle rule, this library sent 1,920-2,000
// packets, one per message, and fails it.
//
// Latency is logged and not gated: libutp's includes its bridge's loop, which
// waits up to 20 ms for the socket when a write arrives (bridge.cpp), so its
// tail is not libutp's alone. Measured, the median message took 15.3 ms from
// libutp and 14.7 ms from this library.
func TestSmallWritesAgainstLibutp(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	path := pathConfig{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 128 * 1024}
	const count, size = 2000, 100
	const interval = 500 * time.Microsecond
	ref := smallWrites(t, true, path, count, size, interval)
	ours := smallWrites(t, false, path, count, size, interval)
	for _, r := range []struct {
		who string
		res smallResult
	}{{"libutp", ref}, {"this library", ours}} {
		t.Logf("%s: %d x %d bytes every %v in %d data packets, %v; message latency p50 %v p99 %v max %v",
			r.who, count, size, interval, r.res.dataPackets, r.res.elapsed.Round(time.Millisecond),
			r.res.p50.Round(10*time.Microsecond), r.res.p99.Round(10*time.Microsecond), r.res.max.Round(10*time.Microsecond))
	}
	if float64(ours.dataPackets) > (1+smallWritesSlack)*float64(ref.dataPackets) {
		t.Errorf("this library sent %d data packets for the messages libutp sent in %d", ours.dataPackets, ref.dataPackets)
	}
}

// smallWritesSlack is how many more packets than libutp's this library may
// send. Measured equal; the slack is for where the 0.5 ms writes fall against
// the round trip on a loaded machine.
const smallWritesSlack = 0.05
