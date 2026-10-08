package netem

import (
	"context"
	"runtime"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// TestIdleConnectionMemory bounds what an open connection holds once it has
// carried a little data and gone quiet -- the state a BitTorrent client's
// peer connections spend most of their time in, and, with no idle timeout by
// default, what a vanished peer the application never closes costs.
//
// It was 1.08 MB per connection end: the receive buffer was allocated at its
// full capacity, 1 MB by default, when the connection was set up, and its
// event queue and the socket's held 1,000 and 1,000,000 slots apiece,
// allocated whole. Now about 23 KB, measured over 500 connections; libutp
// holds about 10 KB per connection, plus 67 KB per context (measured with
// mallinfo2 around 500 of its driver's connections that had exchanged 1 KB
// each way). The bound is loose on purpose: heap figures move with whatever
// else the runtime holds, and the defect it guards against is fifty times it.
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
			s, err := pair.SockB.AcceptWithCid(ctx, accCid, utp.NewConnectionConfig())
			if err != nil {
				t.Errorf("accept %d: %v", i, err)
			}
			accepted <- s
		}()
		s, err := pair.SockA.ConnectWithCid(ctx, iniCid, utp.NewConnectionConfig())
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
	// Let the last acknowledgements settle before measuring.
	time.Sleep(200 * time.Millisecond)
	after := heap()
	perEnd := (int64(after) - int64(before)) / int64(len(streams))
	t.Logf("%d connection ends that exchanged 1 KB each hold %d bytes of heap apiece", len(streams), perEnd)
	if perEnd > perEndMax {
		t.Errorf("each connection end holds %d bytes of heap, want at most %d", perEnd, perEndMax)
	}
	runtime.KeepAlive(streams)
}
