package utp

import (
	"errors"
	"testing"
	"time"
)

func connectedFixture(t *testing.T) *connection {
	t.Helper()
	const syn, synAck = uint16(100), uint16(101)
	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	cc := newDefaultController(fromConnConfig(conn.config))
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: newSentPacketsWithoutLogger(synAck, cc),
		SendBuf:     newSendBuffer(testBufferSize),
		RecvBuf:     newReceiveBuffer(testBufferSize, syn),
	}
	return conn
}

func writeResult(t *testing.T, ch chan *readOrWriteResult) *readOrWriteResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(time.Second):
		t.Fatal("the write was never answered")
		return nil
	}
}

// A write that reaches the connection after this end's FIN has gone, such as
// one queued just before Close or CloseWrite, is refused with an error
// rather than reporting zero bytes and no error.
func TestWriteAfterOurFinIsRefused(t *testing.T) {
	conn := connectedFixture(t)
	fin := uint16(500)
	conn.state.closing = &closingRecord{LocalFin: &fin}
	ch := make(chan *readOrWriteResult, 1)
	conn.onWrite(&queuedWrite{data: []byte("late"), resultCh: ch})
	r := writeResult(t, ch)
	if !errors.Is(r.Err, ErrNotConnected) || r.Len != 0 {
		t.Fatalf("write after our FIN: %d bytes, err %v; want 0 and ErrNotConnected", r.Len, r.Err)
	}
}

// A write that reaches a connection which closed cleanly, with no error of its
// own, still gets one.
func TestWriteAfterCleanCloseIsRefused(t *testing.T) {
	conn := connectedFixture(t)
	conn.state.stateType = connClosed
	ch := make(chan *readOrWriteResult, 1)
	conn.onWrite(&queuedWrite{data: []byte("late"), resultCh: ch})
	r := writeResult(t, ch)
	if !errors.Is(r.Err, ErrNotConnected) || r.Len != 0 {
		t.Fatalf("write after a clean close: %d bytes, err %v; want 0 and ErrNotConnected", r.Len, r.Err)
	}
}

// A writer part of whose bytes were taken before the connection closed is told
// how many, and that the rest were not.
func TestPendingWriterToldWhatWasTaken(t *testing.T) {
	conn := connectedFixture(t)
	ch := make(chan *readOrWriteResult, 1)
	conn.pendingWrites = append(conn.pendingWrites, &queuedWrite{data: make([]byte, 100), written: 300, resultCh: ch})
	conn.state.stateType = connClosed
	conn.processWrites(time.Now())
	r := writeResult(t, ch)
	if r.Len != 300 || !errors.Is(r.Err, ErrNotConnected) {
		t.Fatalf("pending writer after a clean close: %d bytes, err %v; want 300 and ErrNotConnected", r.Len, r.Err)
	}
}
