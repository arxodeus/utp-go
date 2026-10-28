//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
	"github.com/arxodeus/utp/internal/libutp"
)

// TestInteropUnderAdverseConditions is the interop gate on a damaged path:
// real libutp on its own kernel socket, this library on a real socket, and a
// relay between them that loses, reorders and delays datagrams (relay_test.go).
//
// netem.TestLibutpInteropUnderAdverseConditions covers the same conditions
// with libutp driven by a Go loop on the emulated network. What this adds is
// everything that loop replaces: libutp's own thread, its own clock and
// timeout pass, and our socket's real read and write paths, under loss.
//
// A correctness gate: every byte must arrive, in both directions, on every
// profile, and the profile must have actually damaged the transfer.
func TestInteropUnderAdverseConditions(t *testing.T) {
	if testing.Short() {
		t.Skip("adverse interop over real sockets is not a -short test")
	}
	profiles := adverseProfiles()
	payload := makePayload(256 * 1024)

	for i, p := range profiles {
		for _, dir := range []string{"libutp->go", "go->libutp"} {
			t.Run(p.name+" "+dir, func(t *testing.T) {
				seed := int64(1000 + 10*i)
				var elapsed time.Duration
				var data pathStats
				if dir == "libutp->go" {
					elapsed, data = adverseLibutpToGo(t, p.cfg, payload, seed)
				} else {
					elapsed, data = adverseGoToLibutp(t, p.cfg, payload, seed, utp.NewConnectionConfig())
				}
				logAdverse(t, dir, p.name, p.cfg, len(payload), elapsed, data)
			})
		}
	}
}

// TestInteropUnderAdverseConditionsWithRACK is the same gate for this
// library sending with ConnectionConfig.RACK to real libutp: it resends
// different packets at different times, and libutp's receiver has to take
// them. Several seeds a profile, since which packets it resends depends on
// which are lost.
func TestInteropUnderAdverseConditionsWithRACK(t *testing.T) {
	if testing.Short() {
		t.Skip("adverse interop over real sockets is not a -short test")
	}
	payload := makePayload(256 * 1024)
	cfg := utp.NewConnectionConfig()
	cfg.RACK = true
	for i, p := range adverseProfiles() {
		for s := 0; s < 3; s++ {
			seed := int64(2000 + 10*i + s)
			t.Run(fmt.Sprintf("%s seed %d", p.name, seed), func(t *testing.T) {
				elapsed, data := adverseGoToLibutp(t, p.cfg, payload, seed, cfg)
				logAdverse(t, "go->libutp (RACK)", p.name, p.cfg, len(payload), elapsed, data)
			})
		}
	}
}

type adverseProfile struct {
	name string
	cfg  pathConfig
}

func adverseProfiles() []adverseProfile {
	base := pathConfig{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024}
	with := func(f func(*pathConfig)) pathConfig { c := base; f(&c); return c }
	return []adverseProfile{
		{"5% loss", with(func(c *pathConfig) { c.LossRate = 0.05 })},
		{"5% reordering", with(func(c *pathConfig) { c.ReorderRate = 0.05; c.ReorderHold = 20 * time.Millisecond })},
		{"jitter half the delay", with(func(c *pathConfig) { c.Jitter = 10 * time.Millisecond })},
		// Everything at once, on a queue too shallow to absorb it.
		{"bad path", pathConfig{
			Delay: 40 * time.Millisecond, Jitter: 10 * time.Millisecond,
			BandwidthBps: 5_000_000, QueueBytes: 16 * 1024,
			LossRate: 0.03, ReorderRate: 0.02, ReorderHold: 20 * time.Millisecond,
		}},
	}
}

// logAdverse logs a transfer and fails a profile that did not damage it.
func logAdverse(t *testing.T, dir, name string, cfg pathConfig, n int, elapsed time.Duration, data pathStats) {
	t.Helper()
	t.Logf("%s over %s: %d bytes verified in %v (%.2f Mbps); data path %+v",
		dir, name, n, elapsed.Round(time.Millisecond),
		float64(n)*8/elapsed.Seconds()/1e6, data)
	// A profile that stopped damaging packets would pass while testing a
	// clean path.
	if cfg.LossRate > 0 && data.DroppedByLoss == 0 {
		t.Errorf("no datagram was lost on a path configured to drop %.0f%%", cfg.LossRate*100)
	}
	if cfg.ReorderRate > 0 && data.Reordered == 0 {
		t.Errorf("no datagram was reordered on a path configured to reorder %.0f%%", cfg.ReorderRate*100)
	}
}

// adverseLibutpToGo sends payload from libutp to our socket through a relay
// impairing both directions with cfg, and returns the time to the last byte
// and what the data direction did.
func adverseLibutpToGo(t *testing.T, cfg pathConfig, payload []byte, seed int64) (time.Duration, pathStats) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()
	peer, err := libutp.NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Shutdown()
	r := newRelay(t, peer.Port(), goPort, cfg, cfg, seed)
	defer r.Close()

	type result struct {
		data []byte
		at   time.Time
		err  error
	}
	results := make(chan result, 1)
	go func() {
		stream, err := sock.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			results <- result{err: err}
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			results <- result{err: err}
			return
		}
		results <- result{data: buf[:min(n, len(buf))], at: time.Now()}
	}()
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := peer.Connect(r.LibutpFacingPort()); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.WaitState(30*time.Second, libutp.StateConnected); err != nil {
		t.Fatalf("libutp could not connect through the relay: %v; relay to us %+v, to libutp %+v",
			err, r.toGo.Stats(), r.toLib.Stats())
	}
	if _, err := peer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := peer.WaitDrained(90 * time.Second); err != nil {
		t.Fatalf("libutp could not hand off its payload: %v; relay to us %+v", err, r.toGo.Stats())
	}
	peer.CloseStream()

	select {
	case res := <-results:
		if res.err != nil {
			t.Fatalf("our side failed to receive from libutp: %v; relay to us %+v", res.err, r.toGo.Stats())
		}
		if !bytes.Equal(res.data, payload) {
			t.Fatalf("received %d of %d bytes, or the wrong ones", len(res.data), len(payload))
		}
		return res.at.Sub(start), r.toGo.Stats()
	case <-time.After(90 * time.Second):
		t.Fatalf("our side never reached the end of libutp's stream; relay to us %+v, to libutp %+v",
			r.toGo.Stats(), r.toLib.Stats())
	}
	return 0, pathStats{}
}

// adverseGoToLibutp sends payload from our socket to libutp through the relay.
func adverseGoToLibutp(t *testing.T, cfg pathConfig, payload []byte, seed int64, connCfg *utp.ConnectionConfig) (time.Duration, pathStats) {
	t.Helper()
	peer, err := libutp.NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Shutdown()
	peer.Listen()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()
	r := newRelay(t, peer.Port(), goPort, cfg, cfg, seed)
	defer r.Close()

	const initiatorCid, responderCid = 4100, 4101
	cid := utp.NewConnectionID(utp.NewUDPPeer(r.GoFacingAddr()), initiatorCid, responderCid)

	start := time.Now()
	stream, err := sock.ConnectWithCID(ctx, cid, connCfg)
	if err != nil {
		t.Fatalf("could not connect to libutp through the relay: %v; relay to libutp %+v, to us %+v",
			err, r.toLib.Stats(), r.toGo.Stats())
	}
	defer stream.Close()
	writeErr := make(chan error, 1)
	go func() {
		_, err := stream.Write(ctx, payload)
		writeErr <- err
	}()

	got, err := peer.ReadFull(len(payload), 90*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("libutp read %d of %d bytes: %v; relay to libutp %+v, to us %+v",
			len(got), len(payload), err, r.toLib.Stats(), r.toGo.Stats())
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("libutp received %d bytes that do not match what we sent", len(got))
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("our write: %v", err)
	}
	return elapsed, r.toLib.Stats()
}
