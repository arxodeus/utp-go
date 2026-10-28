package utp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// CloseRead wakes a Read already waiting on an open, silent stream, as
// net.TCPConn's does.
func TestCloseReadWakesABlockedRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, _, server := restingPair(t, ctx, NewConnectionConfig())

	errs := make(chan error, 2)
	go func() {
		_, err := server.Read(context.Background(), make([]byte, 16))
		errs <- err
	}()
	go func() {
		var buf []byte
		_, err := server.ReadToEOF(context.Background(), &buf)
		errs <- err
	}()
	time.Sleep(100 * time.Millisecond) // both waiting
	if err := server.CloseRead(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if !errors.Is(err, ErrReadClosed) {
				t.Fatalf("read returned %v, want ErrReadClosed", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a read waiting on a silent stream ignored CloseRead")
		}
	}
}

// A CloseRead whose event was dropped because the event queue was full still
// shuts the read side: data arriving afterwards is acknowledged and
// discarded, not buffered.
func TestCloseReadAppliesWithoutItsEvent(t *testing.T) {
	stream, conn, clk, done := liveAccepted(t)
	defer done()
	const peerID, peerSeq = 6000, 900

	// What CloseRead does when the event cannot be queued: the flag, and a
	// wake, but no streamCloseRead.
	stream.readClosed.Store(true)
	clk.AwaitReactionTo(func() {
		stream.wakeLoop()
		conn.inject(newPacketBuilder(st_data, peerID+1, 120000, 1<<20, peerSeq+2).
			WithAckNum(0x4321 - 1).WithPayload(make([]byte, 1000)).Build().Encode())
	})
	c := stream.conn
	c.mu.Lock()
	shut, readable := c.readShutdown, c.state.RecvBuf.Readable()
	c.mu.Unlock()
	if !shut || readable != 0 {
		t.Fatalf("read side shut %v, %d bytes buffered; want shut and none", shut, readable)
	}
}
