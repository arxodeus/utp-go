package netem

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// failingConn is an Endpoint whose reads fail on demand: the next pending
// reads return errRead, and once broken is set every read does.
type failingConn struct {
	*Endpoint
	pending atomic.Int32
	broken  atomic.Bool
}

var errRead = errors.New("netem: injected read error")

func (c *failingConn) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	if c.broken.Load() {
		time.Sleep(time.Millisecond)
		return 0, nil, errRead
	}
	if c.pending.Add(-1) >= 0 {
		return 0, nil, errRead
	}
	return c.Endpoint.ReadFrom(b)
}

// A read error that does not say it is temporary no longer stops the
// socket from reading. go-libutp, go-utp and rust-utp each tolerate a run of
// 100. This one stopped at the first: every connection on the socket then
// retransmitted into silence until it timed out.
func TestSocketReadsOnAfterAReadError(t *testing.T) {
	n := NewNetwork(91)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: 5 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fa := &failingConn{Endpoint: a}
	fa.pending.Store(5)
	sa := utp.WithSocket(ctx, fa, quiet())
	defer sa.Close()
	sb := utp.WithSocket(ctx, b, quiet())
	defer sb.Close()
	go func() {
		s, err := sb.Accept(ctx, utp.NewConnectionConfig())
		if err == nil {
			_, _ = s.Write(ctx, []byte("hello"))
			s.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()
	c, err := sa.ConnectWithCid(dialCtx, utp.NewConnectionId(b.Addr(), 900, 901), utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("after five read errors the socket no longer reads: %v", err)
	}
	buf := make([]byte, 0, 16)
	if _, err := c.ReadToEOF(dialCtx, &buf); err != nil || string(buf) != "hello" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

// A socket that cannot read at all closes, so a dial waiting on it fails at
// once rather than waiting out its context against a socket that will never
// hear an answer.
func TestSocketThatCannotReadCloses(t *testing.T) {
	n := NewNetwork(92)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: 5 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fa := &failingConn{Endpoint: a}
	fa.broken.Store(true)
	sa := utp.WithSocket(ctx, fa, quiet())
	defer sa.Close()

	start := time.Now()
	_, err := sa.ConnectWithCid(ctx, utp.NewConnectionId(b.Addr(), 900, 901), utp.NewConnectionConfig())
	took := time.Since(start)
	t.Logf("the dial returned %v after %v", err, took.Round(time.Millisecond))
	if err == nil {
		t.Fatal("a dial on a socket that cannot read succeeded")
	}
	if took > 5*time.Second {
		t.Errorf("the dial took %v to fail; a socket that cannot read should close", took)
	}
}
