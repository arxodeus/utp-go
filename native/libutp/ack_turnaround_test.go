//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// TestAckTurnaround measures how long each implementation's receiver takes to
// acknowledge a data packet: from the moment the relay writes the packet to
// the receiver's socket to the moment the relay reads the first
// acknowledgement that covers it, cumulatively or in a selective ack.
//
// COMPATIBILITY.md said this could not be measured, because both
// implementations send deferred acknowledgements when something outside them
// says so -- libutp when its embedder calls utp_issue_deferred_acks, this
// library when an event-loop pass ends -- so a test would time the harness.
// Over real sockets the objection goes away: each runs with its production
// trigger. libutp's bridge drains its socket and then issues the deferred
// acks, which is what utp.h asks of an embedder; this library runs on utpnet.
// Both are timed on the relay's one clock, so the kernel and the relay add the
// same to both.
//
// The sender is held fixed while the receiver changes, so that a difference
// between two rows with the same sender belongs to the receiver. The gate is
// that, for each sender and path, our receiver's 90th-percentile turnaround
// stays within ackTurnaroundSlack of libutp's. Delaying our flush by 1 ms
// fails it (p90 2.7 ms against 0.14 ms), and leaves libutp's rows unchanged.
func TestAckTurnaround(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	paths := []struct {
		name string
		cfg  pathConfig
		size int
	}{
		{"10 Mb/s, 20 ms", pathConfig{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024}, 1 << 20},
		{"100 Mb/s, 1 ms", pathConfig{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024}, 4 << 20},
	}
	for _, p := range paths {
		payload := makePayload(p.size)
		// Each pairing runs ackTurnaroundRounds times, the pairings
		// interleaved, and the gate compares the median of each one's p90s.
		// Run once each and in sequence, a burst of load from elsewhere on
		// the machine landed on one receiver and not the other: under the
		// full suite, with netem running alongside, our p90 read 1.9 ms
		// against libutp's 0.55 ms on a run that read 0.28 ms against 0.12 ms
		// alone.
		results := map[string][]turnaroundStats{}
		for round := 0; round < ackTurnaroundRounds; round++ {
			for _, pair := range []string{"libutp->libutp", "libutp->go", "go->libutp", "go->go"} {
				t.Run(fmt.Sprintf("%s %s #%d", p.name, pair, round), func(t *testing.T) {
					var data, acks []relayEvent
					onNewRelay = func(r *relay) {
						data, acks = nil, nil
						// Data runs sideA -> sideB (toGo) when the sender faces
						// sideA, which it does in every pairing but go->libutp.
						if pair == "go->libutp" {
							r.toLib.delivered, r.toGo.trace = &data, &acks
						} else {
							r.toGo.delivered, r.toLib.trace = &data, &acks
						}
					}
					defer func() { onNewRelay = nil }()
					runPair(t, pair, p.cfg, payload)
					s := turnaround(data, acks)
					if s.unacked > 0 {
						t.Errorf("%d data packets were never acknowledged", s.unacked)
					}
					if s.packets > 0 {
						results[pair] = append(results[pair], s)
					}
					t.Logf("%-15s %-15s ack turnaround p50 %v p90 %v p99 %v max %v; %d data packets, %.2f acks each; %d never acked",
						p.name, pair, s.p50, s.p90, s.p99, s.max, s.packets, s.acksPerPacket, s.unacked)
				})
			}
		}
		for _, sender := range []string{"libutp", "go"} {
			ours, ref := results[sender+"->go"], results[sender+"->libutp"]
			if len(ours) == 0 || len(ref) == 0 {
				continue // a subtest failed and said why
			}
			if o, r := medianP90(ours), medianP90(ref); o > r+ackTurnaroundSlack {
				t.Errorf("%s, %s sending: our receiver acknowledges in %v at the 90th percentile, libutp's in %v (medians of %d and %d runs)",
					p.name, sender, o, r, len(ours), len(ref))
			}
		}
	}
}

// ackTurnaroundRounds is how many times each pairing runs.
const ackTurnaroundRounds = 3

// medianP90 is the median of the runs' 90th percentiles.
func medianP90(runs []turnaroundStats) time.Duration {
	p := make([]time.Duration, len(runs))
	for i, r := range runs {
		p[i] = r.p90
	}
	sort.Slice(p, func(i, j int) bool { return p[i] < p[j] })
	return p[len(p)/2]
}

// ackTurnaroundSlack is how much slower than libutp's our receiver's p90 may
// be. Measured, the two are within about 0.2 ms of each other either way.
const ackTurnaroundSlack = time.Millisecond

type turnaroundStats struct {
	p50, p90, p99, max time.Duration
	packets, unacked   int
	acksPerPacket      float64
}

// turnaround pairs each data packet delivered to the receiver with the first
// acknowledgement it sent afterwards that covers the packet.
func turnaround(data, acks []relayEvent) turnaroundStats {
	var lat []time.Duration
	seen := map[uint16]bool{}
	st := turnaroundStats{}
	j := 0
	for _, d := range data {
		if d.Type != 0 || seen[d.Seq] {
			continue
		}
		seen[d.Seq] = true
		st.packets++
		for j < len(acks) && acks[j].At.Before(d.At) {
			j++
		}
		found := false
		for k := j; k < len(acks); k++ {
			if covers(acks[k], d.Seq) {
				lat = append(lat, acks[k].At.Sub(d.At))
				found = true
				break
			}
		}
		if !found {
			st.unacked++
		}
	}
	nacks := 0
	for _, a := range acks {
		if a.Type == 2 {
			nacks++
		}
	}
	if st.packets > 0 {
		st.acksPerPacket = float64(nacks) / float64(st.packets)
	}
	if len(lat) == 0 {
		return st
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	q := func(f float64) time.Duration { return lat[int(f*float64(len(lat)-1))].Round(10 * time.Microsecond) }
	st.p50, st.p90, st.p99, st.max = q(0.5), q(0.9), q(0.99), lat[len(lat)-1].Round(10*time.Microsecond)
	return st
}

// covers reports whether an acknowledgement acknowledges seq, cumulatively or
// selectively. Selective-ack bit i, least significant first, is ack + 2 + i.
func covers(a relayEvent, seq uint16) bool {
	if int16(a.Ack-seq) >= 0 {
		return true
	}
	off := int(seq - a.Ack - 2)
	if off < 0 || off/8 >= len(a.Sack) {
		return false
	}
	return a.Sack[off/8]&(1<<(off%8)) != 0
}

// runPair runs one verified transfer through a relay with cfg in both
// directions.
func runPair(t *testing.T, pair string, cfg pathConfig, payload []byte) {
	t.Helper()
	switch pair {
	case "libutp->go":
		adverseLibutpToGo(t, cfg, payload, 1)
	case "go->libutp":
		adverseGoToLibutp(t, cfg, payload, 1)
	case "libutp->libutp":
		a, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Shutdown()
		b, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Shutdown()
		b.Listen()
		r := newRelay(t, a.Port(), b.Port(), cfg, cfg, 1)
		defer r.Close()
		if err := a.Connect(r.LibutpFacingPort()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.WaitState(30*time.Second, libutp.StateConnected); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Write(payload); err != nil {
			t.Fatal(err)
		}
		got, err := b.ReadFull(len(payload), 90*time.Second)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("libutp->libutp: %d of %d bytes: %v", len(got), len(payload), err)
		}
	case "go->go":
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		sender, senderPort := bindGoSocket(t, ctx)
		defer sender.Close()
		receiver, receiverPort := bindGoSocket(t, ctx)
		defer receiver.Close()
		// The relay's libutp-facing side faces our sender here.
		r := newRelay(t, senderPort, receiverPort, cfg, cfg, 1)
		defer r.Close()
		done := make(chan error, 1)
		go func() {
			stream, err := receiver.Accept(ctx, utp.NewConnectionConfig())
			if err != nil {
				done <- err
				return
			}
			defer stream.Close()
			buf := make([]byte, 0, len(payload))
			n, err := stream.ReadToEOF(ctx, &buf)
			if err != nil && err != io.EOF {
				done <- err
				return
			}
			if !bytes.Equal(buf[:min(n, len(buf))], payload) {
				done <- fmt.Errorf("received %d bytes that do not match", n)
				return
			}
			done <- nil
		}()
		cid := utp.NewConnectionId(utp.NewUdpPeer(loopback(r.LibutpFacingPort())), 4200, 4201)
		stream, err := sender.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(ctx, payload); err != nil {
			t.Fatal(err)
		}
		stream.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
