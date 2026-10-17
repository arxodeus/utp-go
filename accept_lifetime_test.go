package utp

import (
	"context"
	"testing"
	"time"
)

// An Accept already queued ends when its socket does, whatever the caller's
// context. It watched only the caller's, so one on context.Background()
// outlived the socket: the dispatcher that would have answered it had gone.
// From the release review of 9c44c4f (R7).
func TestAcceptObservesSocketCancellation(t *testing.T) {
	socketCtx, socketCancel := context.WithCancel(context.Background())
	defer socketCancel()
	s := &UtpSocket{ctx: socketCtx, accepts: make(chan *acceptRequest)}
	result := make(chan error, 1)
	go func() {
		_, err := s.Accept(context.Background(), nil)
		result <- err
	}()
	<-s.accepts // the dispatcher takes the request, then the socket ends
	socketCancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Accept succeeded on a cancelled socket")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept ignored its socket's cancellation after queueing")
	}
}

// The same through Close, for both kinds of Accept.
func TestAcceptReturnsWhenTheSocketCloses(t *testing.T) {
	ctx := context.Background()
	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())

	errs := make(chan error, 2)
	go func() {
		_, err := sock.Accept(context.Background(), nil)
		errs <- err
	}()
	go func() {
		_, err := sock.AcceptWithCid(context.Background(), NewConnectionId(conn.peer, 10, 11), nil)
		errs <- err
	}()
	time.Sleep(100 * time.Millisecond) // both queued
	sock.Close()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("an Accept succeeded on a closed socket")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("an Accept outlived Close")
		}
	}
}

// Accepts that time out on a socket nobody connects to are not kept. They
// were dropped only when a SYN came to claim one, so an accept loop with a
// timeout grew the queue by one per timeout for as long as it ran.
func TestTimedOutAcceptsAreNotRetained(t *testing.T) {
	ctx := context.Background()
	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	for i := 0; i < 500; i++ {
		actx, cancel := context.WithTimeout(ctx, time.Millisecond)
		if _, err := sock.Accept(actx, nil); err == nil {
			t.Fatal("an Accept succeeded with nobody connecting")
		}
		cancel()
	}
	sock.dispatchMu.Lock()
	held := len(sock.pendingAccepts)
	sock.dispatchMu.Unlock()
	if held > 1 {
		t.Fatalf("%d abandoned accepts retained after 500 timeouts, want at most 1", held)
	}
}
