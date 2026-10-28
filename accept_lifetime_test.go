package utp

import (
	"context"
	"net"
	"testing"
	"time"
)

// An Accept already queued ends when its socket does, whatever the caller's
// context.
func TestAcceptObservesSocketCancellation(t *testing.T) {
	socketCtx, socketCancel := context.WithCancel(context.Background())
	defer socketCancel()
	s := &Socket{ctx: socketCtx, accepts: make(chan *acceptRequest)}
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
	sock := NewSocket(ctx, conn, conformanceLogger())

	errs := make(chan error, 2)
	go func() {
		_, err := sock.Accept(context.Background(), nil)
		errs <- err
	}()
	go func() {
		_, err := sock.AcceptWithCID(context.Background(), NewConnectionID(conn.peer, 10, 11), nil)
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

// Accepts that time out on a socket nobody connects to are not retained.
func TestTimedOutAcceptsAreNotRetained(t *testing.T) {
	ctx := context.Background()
	conn := newScriptedConn()
	defer conn.Close()
	sock := NewSocket(ctx, conn, conformanceLogger())
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

// A dial given a nil config gets the defaults, as an accept does.
func TestConnectWithANilConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bind := func() *Socket {
		s, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	sa, sb := bind(), bind()
	go sb.Accept(ctx, nil)
	s, err := sa.Connect(ctx, NewUDPPeer(sb.LocalAddr().(*net.UDPAddr)), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
