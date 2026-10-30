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

	utp "github.com/arxodeus/utp"
	"github.com/arxodeus/utp/internal/integration/libutp"
)

// TestAckTurnaround measures how long each implementation's receiver takes to
// acknowledge a data packet: from the relay writing the packet to the
// receiver's socket to the relay reading the first acknowledgement that
// covers it, cumulatively or selectively. Each runs with its production
// trigger for deferred acknowledgements over real sockets: libutp's bridge
// drains its socket and then issues deferred acks, and this library runs on
// utpnet. Both are timed on the relay's clock.
//
// The sender is held fixed while the receiver changes, so a difference
// between rows with the same sender belongs to the receiver. The gate is that
// our receiver's median turnaround stays within ackTurnaroundSlack of
// libutp's for each sender and path; delaying our flush by 1 ms fails it on
// every path.
//
// The relay paces each datagram to its time (relaySpin), so packets are not
// delivered in clumps, and runs in a process of its own
// (relay_process_test.go), so its spinning does not keep this process's Go
// runtime awake.
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
		// Each pairing runs ackTurnaroundRounds times, interleaved, and the
		// gate compares each receiver's best median. A tail percentile would
		// measure the machine's load more than the receiver; a defect that
		// delays the flush moves the median too.
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
		gate := t.Errorf
		if raceEnabled {
			gate = t.Logf // timing under the race detector; see raceEnabled
		}
		for _, sender := range []string{"libutp", "go"} {
			ours, ref := results[sender+"->go"], results[sender+"->libutp"]
			if len(ours) == 0 || len(ref) == 0 {
				continue // a subtest failed and said why
			}
			if o, r := bestP50(ours), bestP50(ref); o > r+ackTurnaroundSlack {
				gate("%s, %s sending: our receiver's median acknowledgement takes %v, libutp's %v (best of %d and %d runs)",
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
// may be. With the relay in its own process, and stamping deliveries before
// its write (relayPath.deliver), ours is 10-20 us slower (12 runs each: 70-80
// us against 50-60 at 10 Mb/s, 40 against 30 at 100 Mb/s), the Go runtime
// waking the socket's reader and a slower turn-round. When an
// acknowledgement waited for up to four packets at 100 Mb/s whatever the
// return path, ours was 220-240 us against 30, which this fails.
const ackTurnaroundSlack = 150 * time.Microsecond

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
		adverseGoToLibutp(t, cfg, payload, 1, utp.NewConnectionConfig())
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
		cid := utp.NewConnectionID(utp.NewUDPPeer(loopback(r.LibutpFacingPort())), 4200, 4201)
		stream, err := sender.ConnectWithCID(ctx, cid, utp.NewConnectionConfig())
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
