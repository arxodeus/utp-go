package netem

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// Data both ways at once over a lossy path.
//
// Nothing else in the suite sends in both directions on one connection, and
// that is where the peer's acknowledgements stop arriving as ST_STATE: they
// ride on its data packets instead. The writer used to wake only for an
// ST_STATE, so in about one run in five one direction sat with a megabyte
// queued, nothing in flight and room in its window, until a timer happened to
// fire some 30 seconds later. libutp wakes on any packet (utp_internal.cpp:
// 2302-2308). With eight runs, a one-in-five stall shows in about five
// attempts out of six.
func TestTwoWayTransferOverLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	const (
		size  = 1 << 20
		runs  = 8
		bound = 15 * time.Second // the median is about 5 s; the stall was about 36
	)
	cfg := Config{Delay: 20 * time.Millisecond, LossRate: 0.05, BandwidthBps: 20_000_000, QueueBytes: 128 * 1024}
	var times []time.Duration
	for seed := 0; seed < runs; seed++ {
		el, err := twoWayTransfer(int64(2000+seed), cfg, size)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		times = append(times, el)
		if el > bound {
			t.Errorf("seed %d: 1 MB each way took %v; one direction stalled", seed, el)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("1 MB each way, 20 Mb/s, 20 ms, 5%% loss, %d runs: median %v, max %v",
		runs, times[len(times)/2].Round(time.Millisecond), times[len(times)-1].Round(time.Millisecond))
}

// twoWayTransfer opens one connection and has both ends write size bytes and
// read the other's size bytes at the same time.
func twoWayTransfer(seed int64, cfg Config, size int) (time.Duration, error) {
	n := NewNetwork(seed)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.ConnectAsymmetric(a, b, cfg, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	var sa, sb *utp.UtpStream
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sb, errB = pair.SockB.AcceptWithCid(ctx, utp.NewConnectionId(a.Addr(), 501, 500), utp.NewConnectionConfig())
	}()
	go func() {
		defer wg.Done()
		sa, errA = pair.SockA.ConnectWithCid(ctx, utp.NewConnectionId(b.Addr(), 500, 501), utp.NewConnectionConfig())
	}()
	wg.Wait()
	if errA != nil || errB != nil {
		return 0, fmt.Errorf("connect: %v, accept: %v", errA, errB)
	}
	defer sa.Close()
	defer sb.Close()

	data := make([]byte, size)
	var mu sync.Mutex
	var failed error
	fail := func(err error) {
		mu.Lock()
		if failed == nil {
			failed = err
		}
		mu.Unlock()
	}
	start := time.Now()
	side := func(s *utp.UtpStream) {
		defer wg.Done()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Write(ctx, data); err != nil {
				fail(fmt.Errorf("write: %w", err))
			}
		}()
		buf := make([]byte, 64*1024)
		for got := 0; got < size; {
			k, err := s.Read(ctx, buf)
			got += k
			if err != nil {
				fail(fmt.Errorf("read after %d bytes: %w", got, err))
				return
			}
		}
	}
	wg.Add(2)
	go side(sa)
	go side(sb)
	wg.Wait()
	return time.Since(start), failed
}
