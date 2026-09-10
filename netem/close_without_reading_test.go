package netem

import (
	"context"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// Closing from a consumer that never read must not hang.
//
// SKIPPED: this reproduces an open defect. It is kept because it is a reliable
// reproduction -- it failed 3 times out of 3 at the 20s mark -- and the next
// attempt at a fix should start by unskipping it.
//
// UtpStream.Close waits for the connection goroutine to finish. That
// goroutine's shutdown path runs processReads, which hands bytes to the reader
// with a blocking channel send. A consumer that accepted a stream, never read
// it, and then closed it deadlocks against itself: Close waits for the loop,
// the loop waits for the reader, and the stream context that would break the
// tie is cancelled by Close only after its wait returns.
//
// libutp has no equivalent. utp_call_on_read hands the embedder its bytes and
// returns; a slow application closes the receive window and never stops the
// protocol.
//
// An attempted fix -- stop filling the read queue once it is full and leave the
// rest in the receive buffer, where it shrinks the advertised window -- is
// written up in KNOWN-LIMITATIONS.md. It fixed this test and broke the
// library: a full parallel suite deadlocked elsewhere, with libutp interop
// reporting "our implementation never finished reading from libutp". It was
// reverted.
func TestCloseWithoutReadingDoesNotHang(t *testing.T) {
	t.Skip("reproduces an open defect: a consumer that never reads deadlocks Close; " +
		"see KNOWN-LIMITATIONS.md, 'The event loop blocks on the application'")

	n := NewNetwork(32)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        5 * time.Millisecond,
		BandwidthBps: 50_000_000,
		QueueBytes:   64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sendSock := utp.WithSocket(ctx, a, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	const initiatorCid, responderCid = 500, 501
	acceptCid := utp.NewConnectionId(a.Addr(), responderCid, initiatorCid)
	connectCid := utp.NewConnectionId(b.Addr(), initiatorCid, responderCid)

	payload := make([]byte, 2<<20)

	accepted := make(chan *utp.UtpStream, 1)
	go func() {
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- stream
	}()

	go func() {
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		_, _ = stream.Write(ctx, payload)
	}()

	var stream *utp.UtpStream
	select {
	case stream = <-accepted:
	case <-time.After(20 * time.Second):
		t.Fatal("accept never completed")
	}
	if stream == nil {
		t.Fatal("accept failed")
	}

	// Let the sender fill the receive buffer and the read queue behind it.
	time.Sleep(1500 * time.Millisecond)

	closed := make(chan struct{})
	go func() { stream.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(20 * time.Second):
		t.Fatal("Close() from a consumer that never read did not return")
	}
}
