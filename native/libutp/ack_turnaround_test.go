//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
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
// that, for each sender and path, our receiver's median turnaround stays
// within ackTurnaroundSlack of libutp's. Delaying our flush by 1 ms fails it
// on every path (medians 0.76-1.17 ms against 0.06-0.18 ms), and leaves
// libutp's rows unchanged.
//
// The relay paces each datagram to its time (relaySpin). Before it did, it
// delivered a 100 Mb/s path's packets in clumps of about ten, and libutp's
// loop, draining each clump at once, appeared to send 0.22-0.33
// acknowledgements per data packet; paced, it sends 0.58-0.62.
//
// The relay runs in a process of its own (relay_process_test.go). Spinning in
// this one, it kept the Go runtime awake, and a runtime with a goroutine
// running wakes the socket's reader sooner than an idle one: our receiver's
// median was 80 us with the relay here and is 90 us without it, against
// libutp's 50 either way, and our 90th percentile, 240 us here, is libutp's
// 1.1 ms without it. An application's runtime is not kept awake for it.
func TestAckTurnaround(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	defer func(was bool) { relayInOwnProcess = was }(relayInOwnProcess)
	relayInOwnProcess = true
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
		// interleaved, and the gate compares each receiver's best median.
		//
		// It used to compare the 90th percentile, and failed three times
		// under the full suite -- netem's CPU-heavy tests run alongside --
		// with our p90 at 1.9, 2.8 and 3.7 ms against libutp's 0.55-1.16 ms,
		// on code that measures 0.28 against 0.12 alone. Load lands in the
		// tail, and it lands harder on a receiver that hands each packet
		// between goroutines than on one that handles it inline on a thread,
		// so the tail measured the machine. A defect that delays the flush
		// delays every acknowledgement, the median with them: a 1 ms delay
		// moves it by a millisecond, well clear of the slack.
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
					if s.unacked > 0 && strings.HasSuffix(pair, "->go") {
						// Our receiver's rows only. libutp's own pair left one
						// packet unacknowledged inside the trace window twice
						// under the full suite's load; it is the reference,
						// and the harness's window closing on its last
						// acknowledgement is not something this test is about.
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
			if o, r := bestP50(ours), bestP50(ref); o > r+ackTurnaroundSlack {
				t.Errorf("%s, %s sending: our receiver's median acknowledgement takes %v, libutp's %v (best of %d and %d runs)",
					p.name, sender, o, r, len(ours), len(ref))
			}
		}
	}
}

// ackTurnaroundRounds is how many times each pairing runs.
const ackTurnaroundRounds = 3

// bestP50 is the lowest of the runs' medians.
func bestP50(runs []turnaroundStats) time.Duration {
	best := runs[0].p50
	for _, r := range runs[1:] {
		if r.p50 < best {
			best = r.p50
		}
	}
	return best
}

// ackTurnaroundSlack is how much slower than libutp's our receiver's median
// may be. With the relay in its own process, ours is about 40 us slower at
// 10 Mb/s, where both acknowledge every packet, and about 90 us slower at 100
// Mb/s, where ours lets an acknowledgement wait for up to four packets
// (connection.ackEvery) and sends 0.22 per data packet to libutp's 0.47.
const ackTurnaroundSlack = 500 * time.Microsecond

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
