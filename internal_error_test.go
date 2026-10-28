package utp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A connection whose own send bookkeeping refuses a packet ends with the
// reason, and the packet is not sent; it does not panic and take the
// process down.
func TestABrokenSendBookkeepingEndsTheConnectionNotTheProcess(t *testing.T) {
	conn, _ := dupAckConnN(t, 3)
	next := conn.state.SentPackets.NextSeqNum()
	sent := len(conn.socketEvents)

	// Two ahead of the next number: out of order, which the send path never
	// does.
	skipped := newPacketBuilder(st_data, conn.cid.Send, 0, 1<<20, next+2).WithPayload([]byte{1}).Build()
	conn.transmit(skipped, time.Now(), true)

	if conn.state.stateType != connClosed {
		t.Fatalf("the connection is %v after its bookkeeping refused a packet; expected it closed", conn.state.stateType)
	}
	if !errors.Is(conn.state.Err, errInternal) || !errors.Is(conn.state.Err, errOutOfOrderTransmit) {
		t.Fatalf("the connection ended with %v; expected an internal error naming the out-of-order transmit", conn.state.Err)
	}
	if n := len(conn.socketEvents) - sent; n != 0 {
		t.Fatalf("%d packets sent after the bookkeeping refused one", n)
	}

	// The rest of the pass sends nothing either.
	inOrder := newPacketBuilder(st_data, conn.cid.Send, 0, 1<<20, next).WithPayload([]byte{1}).Build()
	conn.transmit(inOrder, time.Now(), true)
	if n := len(conn.socketEvents) - sent; n != 0 {
		t.Fatalf("%d packets sent by a connection that had ended", n)
	}
}

// A refused transmission is not recorded: OnTransmit reports it and leaves the
// window as it was.
func TestOnTransmitPastTheWindowIsRefusedAndNotRecorded(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	sent := newSentPacketsWithoutLogger(100, ctrl)
	window := sent.Window()
	err := sent.OnTransmit(sent.NextSeqNum(), st_data, nil, window+1, time.Now())
	if !errors.Is(err, errTransmitExceedsWindow) {
		t.Fatalf("a transmission of %d bytes into a %d-byte window returned %v", window+1, window, err)
	}
	if sent.HasUnackedPackets() || sent.BytesInFlight() != 0 {
		t.Fatalf("the refused packet was recorded: %d bytes in flight", sent.BytesInFlight())
	}
}

// A dial to a peer whose every id is taken fails rather than searching
// forever.
func TestDialWithNoFreeIdFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	done := make(chan int, 1)
	go func() {
		ch := make(chan *streamEvent)
		taken := 0
		for {
			if _, err := sock.generateCid(conn.peer, true, ch); err != nil {
				done <- taken
				return
			}
			taken++
		}
	}()
	var taken int
	select {
	case taken = <-done:
	case <-time.After(50 * time.Second):
		t.Fatal("still looking for a free id after 50s")
	}
	t.Logf("%d connections to one peer took every id", taken)

	start := time.Now()
	_, err := sock.Connect(ctx, conn.peer, NewConnectionConfig())
	if !errors.Is(err, errNoFreeConnectionId) {
		t.Fatalf("a dial with every id taken returned %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the dial took %v to fail", d)
	}
}
