package netem

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// What Close waits for, measured on a link whose conditions can be changed.
//
// Close used to wait for the connection's event loop to exit, full stop. A
// loop with unacknowledged data does not exit until its retransmission ladder
// runs out -- 1 + 2 + 4 + 8 + 16 seconds -- and where that is not enough, the
// 60-second idle timeout behind it. Closing a connection whose peer had gone
// took 31 seconds in one shape and 60 in another, with the caller held for all
// of it. The standard library's net.Conn suite found it: the eleven subtests
// that kernel TCP finishes in 0.43s took 140 seconds here, and almost all of
// that was Close.
//
// The obvious fix, a flat timeout, breaks the opposite case. Write returns
// once the data is in the send buffer rather than once it is acknowledged
// (conn.go, processWrites), so a write followed by a close has a tail still to
// flush -- and on a slow link that tail takes longer than any timeout short
// enough to be worth having. A clock cannot tell the two apart. Whether the
// peer is still answering can, and that is what Close now watches.
//
// These two tests hold each end of that. Both need a link that can be changed
// underneath a live connection, which is why they are here and not beside the
// socket: on loopback the tail flushes in milliseconds and a closed peer
// politely sends a FIN, so neither case can be staged at all. Written against
// real sockets first, both passed while measuring nothing -- Close returned in
// 8ms with nothing left to send, and in 0s against a "vanished" peer that had
// in fact said goodbye.

// A close on a link too slow to have flushed must still deliver every byte.
//
// The link carries 2 Mbps, so the send buffer's worth of tail takes seconds to
// drain -- comfortably past closeStallTimeout. A flat bound would cut it off
// here, and the peer would be short exactly the bytes Close stopped waiting
// for.
func TestCloseFlushesASlowTail(t *testing.T) {
	n := NewNetwork(4242)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 2_000_000,
		QueueBytes:   256 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sockA := utp.WithSocket(ctx, a, quiet())
	defer sockA.Close()
	sockB := utp.WithSocket(ctx, b, quiet())
	defer sockB.Close()

	const size = 512 * 1024
	payload := make([]byte, size)
	rng := rand.New(rand.NewSource(99))
	rng.Read(payload)

	type result struct {
		data []byte
		err  error
	}
	got := make(chan result, 1)
	go func() {
		stream, err := sockB.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			got <- result{err: err}
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, size)
		nread, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			got <- result{err: err}
			return
		}
		got <- result{data: buf[:min(nread, len(buf))]}
	}()

	time.Sleep(100 * time.Millisecond)
	cid := utp.NewConnectionId(b.Addr(), 8100, 8101)
	stream, err := sockA.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := stream.Write(ctx, payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	start := time.Now()
	stream.Close()
	closeTook := time.Since(start)

	select {
	case res := <-got:
		if res.err != nil {
			t.Fatalf("the peer failed to read: %v", res.err)
		}
		if len(res.data) != size {
			t.Fatalf("the peer received %d of %d bytes after a Close that took %v; the wait "+
				"gave up while there was still something to deliver",
				len(res.data), size, closeTook.Round(time.Millisecond))
		}
		if !bytes.Equal(res.data, payload) {
			t.Fatal("the peer received the right number of bytes, but not the right ones")
		}
	case <-time.After(180 * time.Second):
		t.Fatal("the peer never finished reading")
	}

	// The point of the measurement: this is well past any flat bound that
	// would also have fixed the vanished-peer case.
	if closeTook < 2*time.Second {
		t.Errorf("Close returned in %v, which is inside closeStallTimeout -- the link is no "+
			"longer slow enough for this test to distinguish a stall bound from a clock",
			closeTook.Round(time.Millisecond))
	}
	t.Logf("Close flushed a %d-byte transfer's tail over a 2 Mbps link in %v, delivering all of it",
		size, closeTook.Round(time.Millisecond))
}

// A close whose peer has gone silent must return promptly.
//
// The link is set to drop everything in both directions before the close, so
// nothing answers and nothing refuses either: no acknowledgement, no FIN, no
// RESET. That is what a peer that was unplugged looks like, and it is the case
// that cost 31 seconds.
func TestCloseDoesNotWaitForASilentPeer(t *testing.T) {
	n := NewNetwork(4243)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	linkCfg := Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   256 * 1024,
	}
	n.Connect(a, b, linkCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sockA := utp.WithSocket(ctx, a, quiet())
	defer sockA.Close()
	sockB := utp.WithSocket(ctx, b, quiet())
	defer sockB.Close()

	accepted := make(chan *utp.UtpStream, 1)
	go func() {
		stream, err := sockB.Accept(ctx, utp.NewConnectionConfig())
		if err == nil {
			accepted <- stream
		}
	}()

	time.Sleep(100 * time.Millisecond)
	cid := utp.NewConnectionId(b.Addr(), 8200, 8201)
	stream, err := sockA.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	select {
	case <-accepted:
	case <-time.After(30 * time.Second):
		t.Fatal("no connection was accepted")
	}

	payload := make([]byte, 256*1024)
	rand.New(rand.NewSource(100)).Read(payload)
	if _, err := stream.Write(ctx, payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Unplug it. Both directions, so nothing the peer might say gets back
	// either -- a closed socket would have sent a FIN, which is a different
	// case entirely and the one this test kept accidentally measuring.
	blackhole := linkCfg
	blackhole.LossRate = 1
	if err := n.SetConfig("a", "b", blackhole); err != nil {
		t.Fatal(err)
	}
	if err := n.SetConfig("b", "a", blackhole); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	stream.Close()
	elapsed := time.Since(start)

	// Unbounded, this waits out the retransmission ladder (1+2+4+8+16 = 31s)
	// and, when the connection has not given up by then, the 60-second idle
	// timeout behind it -- measured at 60.001s with the bound removed. Two
	// seconds of silence plus whatever the last exchange was still doing is
	// the bound; anything near either of those numbers means it is gone.
	if elapsed > 10*time.Second {
		t.Fatalf("Close took %v for a peer that had gone silent; it is waiting out the "+
			"retransmission ladder again", elapsed.Round(time.Millisecond))
	}
	// And it should not be instant either: there was unacknowledged data, so
	// Close had something to wait for and did wait, rather than returning
	// because the connection had already finished.
	if elapsed < 500*time.Millisecond {
		t.Errorf("Close returned in %v, too fast to have waited for anything; the connection "+
			"had probably already ended, so this is not measuring a silent peer",
			elapsed.Round(time.Millisecond))
	}
	t.Logf("Close on a silent peer returned in %v; unbounded it took 60.001s here, and 31s "+
		"in the net.Conn suite", elapsed.Round(time.Millisecond))
}
