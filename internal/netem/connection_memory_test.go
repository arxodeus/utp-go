package netem

import (
	"context"
	"runtime"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// TestIdleConnectionMemory bounds what an open connection holds once it has
// carried a little data and gone quiet -- the state a BitTorrent client's
// peer connections spend most of their time in, and, with no idle timeout by
// default, what a vanished peer the application never closes costs.
//
// It was 1.08 MB per connection end: the receive buffer was allocated at its
// full capacity, 1 MB by default, when the connection was set up, and its
// event queue and the socket's held 1,000 and 1,000,000 slots apiece,
// allocated whole. Then about 23 KB, and 13.4 KB since the timer and write
// queues shrank, measured over 400 connection ends; libutp
// holds about 10 KB per connection, plus 67 KB per context (measured with
// mallinfo2 around 500 of its driver's connections that had exchanged 1 KB
// each way). The bound is loose on purpose: heap figures move with whatever
// else the runtime holds, and the defect it guards against is fifty times it.
//
// It is measured twice, over two sets of connections: within the keep-alive
// interval, at libutp's 29 seconds, and once connections given a short one
// have been quiet past it, when each has released its receive buffer's
// storage. The heap figure includes this test's own emulated network and
// bookkeeping, about 1.5 KB an end by a heap profile. The goroutine stacks
// are reported beside it, since libutp has no thread per connection and an
// honest comparison counts them; an idle connection holds none now, and
// this fails if it holds a goroutine (dormant.go).
func TestIdleConnectionMemory(t *testing.T) {
	goroutines := runtime.NumGoroutine()
	busy := idleConnectionMemory(t, 0, 200*time.Millisecond)
	// The first set's connections end in the background once their sockets
	// close; measured from before they have, the second baseline holds
	// their teardown and the second figure reads low.
	for deadline := time.Now().Add(30 * time.Second); runtime.NumGoroutine() > goroutines; {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines still running from the first set, %d before it",
				runtime.NumGoroutine(), goroutines)
		}
		time.Sleep(20 * time.Millisecond)
	}
	quiet := idleConnectionMemory(t, idleMemoryKeepAlive, 4*idleMemoryKeepAlive)
	t.Logf("quiet past the keep-alive interval, each end holds %d bytes less", busy-quiet)
	// About 3 KB less in five separate runs, 2.8-3.3 KB, and 0.9 KB with the
	// release disabled. Not asserted: in one process the figures move with
	// what earlier runs left in pools and free lists, by more than that.
	// TestQuietConnectionReleasesReceiveStorage asserts the release itself.
}

// idleConnectionMemory sets up 200 connections that exchange 1 KB each, waits,
// and reports the heap each end holds. A keepAlive of zero is the default.
func idleConnectionMemory(t *testing.T, keepAlive, wait time.Duration) int64 {
	t.Helper()
	const (
		conns     = 200
		perEndMax = 64 * 1024
	)
	when := "within the keep-alive interval"
	if keepAlive > 0 {
		when = "quiet past the keep-alive interval"
	}
	config := func() *utp.ConnectionConfig {
		c := utp.NewConnectionConfig()
		if keepAlive > 0 {
			c.KeepAliveInterval = keepAlive
		}
		return c
	}
	n := NewNetwork(41)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())
	defer pair.SockA.Close()
	defer pair.SockB.Close()

	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapInuse
	}
	before := heap()
	stackBefore, goroutinesBefore := stackInUse(), runtime.NumGoroutine()
	streams := make([]*utp.Stream, 0, 2*conns)
	payload := make([]byte, 1024)
	buf := make([]byte, 2048)
	for i := 0; i < conns; i++ {
		base := uint16(2000 + 4*i)
		accCid := utp.NewConnectionID(a.Addr(), base+1, base)
		iniCid := utp.NewConnectionID(b.Addr(), base, base+1)
		accepted := make(chan *utp.Stream, 1)
		go func() {
			s, err := pair.SockB.AcceptWithCID(ctx, accCid, config())
			if err != nil {
				t.Errorf("accept %d: %v", i, err)
			}
			accepted <- s
		}()
		s, err := pair.SockA.ConnectWithCID(ctx, iniCid, config())
		if err != nil {
			t.Fatalf("connect %d: %v", i, err)
		}
		r := <-accepted
		if r == nil {
			t.FailNow()
		}
		if _, err := s.Write(ctx, payload); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		for got := 0; got < len(payload); {
			k, err := r.Read(ctx, buf)
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			got += k
		}
		streams = append(streams, s, r)
	}
	time.Sleep(wait)
	after := heap()
	perEnd := (int64(after) - int64(before)) / int64(len(streams))
	stackPerEnd := (int64(stackInUse()) - int64(stackBefore)) / int64(len(streams))
	goroutines := runtime.NumGoroutine() - goroutinesBefore
	t.Logf("%s: %d connection ends that exchanged 1 KB each hold %d bytes of heap apiece, "+
		"%d bytes of goroutine stack apiece, and %d goroutines between them",
		when, len(streams), perEnd, stackPerEnd, goroutines)
	if perEnd > perEndMax {
		t.Errorf("%s: each connection end holds %d bytes of heap, want at most %d", when, perEnd, perEndMax)
	}
	// An idle connection gives its goroutine back (dormant.go): libutp has no
	// thread per connection, and the parked goroutine's stack was 8 KB an
	// end. A few may still be on their way out when this looks.
	if goroutines > len(streams)/20 {
		t.Errorf("%s: %d idle connection ends hold %d goroutines; an idle connection should hold none",
			when, len(streams), goroutines)
	}
	runtime.KeepAlive(streams)
	return perEnd
}

// stackInUse is the process's goroutine stack memory, after a collection has
// shrunk what it can. Goroutines that have exited leave their stacks cached,
// counted here, so only the first measurement in a process is clean; the
// figure is reported, not asserted.
func stackInUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.StackInuse
}

// idleMemoryKeepAlive stands in for the 29-second keep-alive interval, after
// which a quiet connection releases its receive buffer's storage.
const idleMemoryKeepAlive = 300 * time.Millisecond
