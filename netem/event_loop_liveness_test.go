package netem

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// A connection's event loop must keep running while its application is not
// reading.
//
// This is the defect's general form; TestCloseWithoutReadingDoesNotHang is one
// consequence of it. libutp cannot reach this state: utp_call_on_read hands the
// embedder its bytes and returns, and a slow application closes the advertised
// receive window rather than stopping the protocol.
//
// Liveness is measured directly rather than inferred from the peer's traffic.
// The metrics callback runs on the event-loop goroutine (see sampleMetrics),
// and a short KeepAliveInterval guarantees the loop wakes on its own even with
// nothing arriving, so the callback firing *is* the loop running. An earlier
// version of this test watched the return path for packets instead, which
// depends on the peer choosing to probe the closed window, and it was flaky in
// both directions.
func TestEventLoopRunsWhileReaderIsStalled(t *testing.T) {
	n := NewNetwork(41)
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

	const initiatorCid, responderCid = 600, 601
	acceptCid := utp.NewConnectionId(a.Addr(), responderCid, initiatorCid)
	connectCid := utp.NewConnectionId(b.Addr(), initiatorCid, responderCid)

	// Every sample is one pass of the receiver's event loop.
	var loopPasses atomic.Uint64
	recvCfg := utp.NewConnectionConfig()
	recvCfg.KeepAliveInterval = 100 * time.Millisecond
	recvCfg.MetricsInterval = 10 * time.Millisecond
	recvCfg.Metrics = func(utp.ConnectionMetrics) { loopPasses.Add(1) }

	payload := make([]byte, 4<<20)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	accepted := make(chan struct{})
	go func() {
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, recvCfg)
		if err != nil {
			t.Errorf("accept: %v", err)
			close(accepted)
			return
		}
		defer stream.Close()
		close(accepted)
		// Never read. Hold the stream open so the connection stays up.
		<-ctx.Done()
	}()

	go func() {
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		_, _ = stream.Write(ctx, payload)
	}()

	select {
	case <-accepted:
	case <-time.After(20 * time.Second):
		t.Fatal("accept never completed")
	}

	// Let the sender fill the receive buffer and the read queue behind it.
	time.Sleep(2 * time.Second)

	before := loopPasses.Load()
	time.Sleep(2 * time.Second)
	after := loopPasses.Load()

	t.Logf("event-loop passes: %d before, %d after a 2s window with no reader", before, after)

	// With a 100ms keep-alive the loop should wake ~20 times in 2 seconds. One
	// is enough to prove it is not blocked.
	if after <= before {
		t.Errorf("the receiver's event loop did not run once in 2 seconds while its "+
			"application was not reading (%d passes at both ends of the window); it is "+
			"blocked handing bytes to the reader, so this connection is not acking, "+
			"not updating its window and not retransmitting", before)
	}
}
