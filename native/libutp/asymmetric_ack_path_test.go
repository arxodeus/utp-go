//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// TestAsymmetricAckPath measures what each receiver's acknowledgements cost a
// libutp sender: libutp sends to libutp, then to this library, over the same
// paced relay, and the time from Connect until the receiver holds every byte
// is compared.
//
// libutp acknowledges once per pass of its embedder's loop, which on a link,
// where packets arrive spaced, is once per packet: 1,750 acknowledgements a
// second for a 20 Mb/s flow, whatever the return path can carry. This
// receiver does the same until its acknowledgements queue on the way back,
// and then lets one cover several packets (connection.ackEvery). The gates
// hold that to its measured effect, 20 runs each:
//
//   - over a 160 kb/s return path, which carries 1,000 acknowledgements a
//     second, our receiver's median time is under asymmetricThinGain of
//     libutp's (measured 2.03 s against 2.99 s);
//   - over a 20 Mb/s return path it is no slower, beyond asymmetricSlack, and
//     sends no more acknowledgements than libutp's, beyond asymmetricAckShare
//     (measured 1.96 s and 2,744 against 1.96 s and 2,769);
//   - at 100 Mb/s and 1 ms both ways, our receiver's best time is no slower
//     than libutp's best beyond asymmetricSlack (measured 1.387 s against
//     1.388 s). Best, not median: libutp's sender fills the relay's 256 KB
//     queue near the end of the transfer -- its delay target is longer than
//     the queue -- and in 6-7 runs of 20, for either receiver, waits out a
//     timeout for the two packets it lost, taking about 2.7 s.
//
// An acknowledgement for every read (ackEvery returning 1) fails the first:
// 3.01 s against libutp's 3.00 s.
func TestAsymmetricAckPath(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	cases := []struct {
		name       string
		data, back pathConfig
		size       int
		thin       bool // gate the median against asymmetricThinGain
		best       bool // gate the best run rather than the median
	}{
		{
			name: "20 Mb/s data, 160 kb/s return",
			data: pathConfig{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 128 * 1024},
			back: pathConfig{Delay: 10 * time.Millisecond, BandwidthBps: 160_000, QueueBytes: 8 * 1024},
			size: 4 << 20, thin: true,
		},
		{
			name: "20 Mb/s both ways",
			data: pathConfig{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 128 * 1024},
			back: pathConfig{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 8 * 1024},
			size: 4 << 20,
		},
		{
			name: "100 Mb/s both ways, 1 ms",
			data: pathConfig{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024},
			back: pathConfig{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024},
			size: 16 << 20, best: true,
		},
	}
	for _, c := range cases {
		payload := makePayload(c.size)
		var times = map[bool][]time.Duration{}
		var acks = map[bool][]uint64{}
		for run := 0; run < asymmetricRuns; run++ {
			for _, toGo := range []bool{false, true} {
				el, offered := libutpSendsTo(t, toGo, c.data, c.back, payload)
				times[toGo] = append(times[toGo], el)
				acks[toGo] = append(acks[toGo], offered)
				t.Logf("%s, libutp->%s run %d: %v, %d packets on the return path",
					c.name, map[bool]string{false: "libutp", true: "go"}[toGo], run, el.Round(time.Millisecond), offered)
			}
		}
		pick := median
		if c.best {
			pick = best
		}
		gate := t.Errorf
		if raceEnabled {
			gate = t.Logf // timing under the race detector; see raceEnabled
		}
		ours, ref := pick(times[true]), pick(times[false])
		switch {
		case c.thin && float64(ours) > asymmetricThinGain*float64(ref):
			gate("%s: our receiver took %v, libutp's %v; want under %.2f of it", c.name, ours, ref, asymmetricThinGain)
		case !c.thin && float64(ours) > (1+asymmetricSlack)*float64(ref):
			gate("%s: our receiver took %v, libutp's %v", c.name, ours, ref)
		}
		if !c.thin && !c.best {
			if o, r := medianCount(acks[true]), medianCount(acks[false]); float64(o) > asymmetricAckShare*float64(r) {
				gate("%s: our receiver sent %d packets back, libutp's %d; want under %.2f of it", c.name, o, r, asymmetricAckShare)
			}
		}
	}
}

const (
	asymmetricRuns     = 3
	asymmetricThinGain = 0.85
	asymmetricSlack    = 0.02
	asymmetricAckShare = 1.05
)

// libutpSendsTo times libutp sending payload to a receiver -- this library's
// when toGo, libutp's otherwise -- through a relay with data toward the
// receiver and back toward the sender, from Connect until the receiver holds
// every byte. It returns that time and how many datagrams the receiver
// offered to the return path.
func libutpSendsTo(t *testing.T, toGo bool, data, back pathConfig, payload []byte) (time.Duration, uint64) {
	t.Helper()
	sender, err := libutp.NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var recvPort uint16
	done := make(chan error, 1)
	var finished time.Time
	if toGo {
		sock, port := bindGoSocket(t, ctx)
		defer sock.Close()
		recvPort = port
		go func() {
			s, err := sock.Accept(ctx, utp.NewConnectionConfig())
			if err != nil {
				done <- err
				return
			}
			defer s.Close()
			buf := make([]byte, 64*1024)
			got := make([]byte, 0, len(payload))
			for len(got) < len(payload) {
				n, err := s.Read(ctx, buf)
				got = append(got, buf[:n]...)
				if err != nil {
					done <- err
					return
				}
			}
			finished = time.Now()
			done <- verifyPayload(got, payload)
		}()
	} else {
		receiver, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatal(err)
		}
		defer receiver.Shutdown()
		receiver.Listen()
		recvPort = receiver.Port()
		go func() {
			got, err := receiver.ReadFull(len(payload), 90*time.Second)
			finished = time.Now()
			if err != nil {
				done <- err
				return
			}
			done <- verifyPayload(got, payload)
		}()
	}

	// The relay's toGo path faces the receiver, whichever it is; toLib carries
	// the receiver's acknowledgements back to the sender.
	onNewRelay = func(r *relay) { r.toLib.cfg = back }
	r := newRelay(t, sender.Port(), recvPort, data, data, 1)
	onNewRelay = nil
	defer r.Close()

	start := time.Now()
	if err := sender.Connect(r.LibutpFacingPort()); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.WaitState(30*time.Second, libutp.StateConnected); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("receiver: %v", err)
		}
	case <-time.After(100 * time.Second):
		t.Fatal("the transfer did not finish")
	}
	return finished.Sub(start), r.toLib.Stats().Offered
}

// verifyPayload reports whether a receiver got exactly what was sent.
func verifyPayload(got, want []byte) error {
	if !bytes.Equal(got, want) {
		return errors.New("received bytes differ from those sent")
	}
	return nil
}

func median(xs []time.Duration) time.Duration {
	s := append([]time.Duration(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func best(xs []time.Duration) time.Duration {
	s := append([]time.Duration(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[0]
}

func medianCount(xs []uint64) uint64 {
	s := append([]uint64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}
