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
// It is measured twice: just after the exchange, and once the connections
// have been quiet for their keep-alive interval, when the receive buffer's
// storage is released. The heap figure includes this test's own emulated
// network and bookkeeping, about 1.5 KB an end by a heap profile; the
// goroutine stacks are reported beside it, since libutp has no thread per
// connection and an honest comparison counts them.
func TestIdleConnectionMemory(t *testing.T) {
	const (
		conns     = 200
		perEndMax = 64 * 1024
	)
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
	streams := make([]*utp.UtpStream, 0, 2*conns)
	payload := make([]byte, 1024)
	buf := make([]byte, 2048)
	for i := 0; i < conns; i++ {
		base := uint16(2000 + 4*i)
		accCid := utp.NewConnectionId(a.Addr(), base+1, base)
		iniCid := utp.NewConnectionId(b.Addr(), base, base+1)
		accepted := make(chan *utp.UtpStream, 1)
		go func() {
			s, err := pair.SockB.AcceptWithCid(ctx, accCid, idleMemoryConfig())
			if err != nil {
				t.Errorf("accept %d: %v", i, err)
			}
			accepted <- s
		}()
		s, err := pair.SockA.ConnectWithCid(ctx, iniCid, idleMemoryConfig())
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
	measure := func(when string) {
		after := heap()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		perEnd := (int64(after) - int64(before)) / int64(len(streams))
		t.Logf("%s: %d connection ends that exchanged 1 KB each hold %d bytes of heap apiece, "+
			"and the process %d bytes of goroutine stack apiece", when, len(streams), perEnd,
			int64(ms.StackInuse)/int64(len(streams)))
		if perEnd > perEndMax {
			t.Errorf("%s: each connection end holds %d bytes of heap, want at most %d", when, perEnd, perEndMax)
		}
	}
	// Let the last acknowledgements settle before measuring.
	time.Sleep(200 * time.Millisecond)
	measure("just after")
	time.Sleep(4 * idleMemoryKeepAlive)
	measure("quiet for the keep-alive interval")
	runtime.KeepAlive(streams)
}

// idleMemoryKeepAlive stands in for the 29-second keep-alive interval, after
// which a quiet connection releases its receive buffer's storage.
const idleMemoryKeepAlive = 300 * time.Millisecond

func idleMemoryConfig() *utp.ConnectionConfig {
	c := utp.NewConnectionConfig()
	c.KeepAliveInterval = idleMemoryKeepAlive
	return c
}
