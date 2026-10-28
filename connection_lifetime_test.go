package utp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// loopbackSockets binds two sockets on the loopback address.
func loopbackSockets(t *testing.T, ctx context.Context) (a, b *Socket) {
	t.Helper()
	lg := icmpQuietLog()
	a, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	b, err = Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	return a, b
}

// An accepted connection belongs to the socket, not to the Accept call that
// returned it, just as a dialed connection outlives its dial: a timeout on
// the Accept's context after it returns does not end the connection.
func TestAcceptedConnectionOutlivesItsAccept(t *testing.T) {
	for _, withCid := range []bool{true, false} {
		name := "Accept"
		if withCid {
			name = "AcceptWithCID"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			sa, sb := loopbackSockets(t, ctx)
			defer sa.Close()
			defer sb.Close()
			aAddr := sa.LocalAddr().(*net.UDPAddr)
			bAddr := sb.LocalAddr().(*net.UDPAddr)
			cidA := NewConnectionID(NewUDPPeer(bAddr), 3000, 3001)
			cidB := NewConnectionID(NewUDPPeer(aAddr), 3001, 3000)

			dialled := make(chan *Stream, 1)
			go func() {
				c, err := sa.ConnectWithCID(ctx, cidA, NewConnectionConfig())
				if err != nil {
					t.Errorf("connect: %v", err)
				}
				dialled <- c
			}()
			// The SYN waits at the server until an Accept claims it.
			for sb.incomingConns.len() == 0 {
				if ctx.Err() != nil {
					t.Fatal("the SYN never arrived")
				}
				time.Sleep(time.Millisecond)
			}

			acceptCtx, endAccept := context.WithCancel(ctx)
			var server *Stream
			var err error
			if withCid {
				server, err = sb.AcceptWithCID(acceptCtx, cidB, NewConnectionConfig())
			} else {
				server, err = sb.Accept(acceptCtx, NewConnectionConfig())
			}
			if err != nil {
				t.Fatalf("accept: %v", err)
			}
			endAccept()
			client := <-dialled
			if client == nil {
				t.FailNow()
			}

			if _, err := client.Write(ctx, []byte("after accept")); err != nil {
				t.Fatalf("write: %v", err)
			}
			readCtx, readDone := context.WithTimeout(ctx, 5*time.Second)
			defer readDone()
			buf := make([]byte, 32)
			n, err := server.Read(readCtx, buf)
			if err != nil || string(buf[:n]) != "after accept" {
				t.Fatalf("read after the Accept's context ended: %q, %v", buf[:n], err)
			}
		})
	}
}

// A connection that ends because its socket was closed says so: the reader
// gets what had already arrived, then ErrSocketClosed, and so does a writer.
func TestSocketCloseIsReportedToTheConnection(t *testing.T) {
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		sa, sb := loopbackSockets(t, ctx)
		aAddr := sa.LocalAddr().(*net.UDPAddr)
		bAddr := sb.LocalAddr().(*net.UDPAddr)
		cidA := NewConnectionID(NewUDPPeer(bAddr), 4000, 4001)
		cidB := NewConnectionID(NewUDPPeer(aAddr), 4001, 4000)
		accepted := make(chan *Stream, 1)
		go func() {
			s, err := sb.AcceptWithCID(ctx, cidB, NewConnectionConfig())
			if err != nil {
				t.Errorf("accept: %v", err)
			}
			accepted <- s
		}()
		client, err := sa.ConnectWithCID(ctx, cidA, NewConnectionConfig())
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		server := <-accepted
		if server == nil {
			t.FailNow()
		}
		if _, err := client.Write(ctx, []byte("queued")); err != nil {
			t.Fatalf("write: %v", err)
		}
		// Arrived and waiting for the reader before the socket goes.
		for len(server.reads) == 0 {
			if ctx.Err() != nil {
				t.Fatal("the data never arrived")
			}
			time.Sleep(time.Millisecond)
		}

		sb.Close()

		buf := make([]byte, 32)
		n, err := server.Read(ctx, buf)
		if err != nil || string(buf[:n]) != "queued" {
			t.Fatalf("run %d: first read after the socket closed: %q, %v; expected what had arrived", i, buf[:n], err)
		}
		if n, err := server.Read(ctx, buf); !errors.Is(err, ErrSocketClosed) || !errors.Is(err, net.ErrClosed) {
			t.Fatalf("run %d: read after the socket closed: %d bytes, %v; expected ErrSocketClosed", i, n, err)
		}
		if _, err := server.Write(ctx, []byte("x")); !errors.Is(err, ErrSocketClosed) {
			t.Fatalf("run %d: write after the socket closed: %v; expected ErrSocketClosed", i, err)
		}
		sa.Close()
		cancel()
	}
}

// A connection handed to an Accept whose caller has already given up is
// closed rather than left running unattended.
func TestConnectionForAnAbandonedAcceptIsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sa, sb := loopbackSockets(t, ctx)
	defer sa.Close()
	defer sb.Close()
	aAddr := sa.LocalAddr().(*net.UDPAddr)
	bAddr := sb.LocalAddr().(*net.UDPAddr)
	cidA := NewConnectionID(NewUDPPeer(bAddr), 5000, 5001)
	cidB := NewConnectionID(NewUDPPeer(aAddr), 5001, 5000)
	accepted := make(chan *Stream, 1)
	go func() {
		s, _ := sb.AcceptWithCID(ctx, cidB, NewConnectionConfig())
		accepted <- s
	}()
	if _, err := sa.ConnectWithCID(ctx, cidA, NewConnectionConfig()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	stream := <-accepted
	if stream == nil {
		t.FailNow()
	}

	// The handover racing the caller's context: the caller gives up first.
	accept := newAccept(ctx, nil, nil)
	accept.giveUp()
	accept.hand(&streamResult{stream: stream})
	if !stream.shutdown.Load() {
		t.Fatal("a connection handed to an Accept that had given up was left open")
	}
	select {
	case r := <-accept.stream:
		t.Fatalf("the connection was queued for a caller that had gone: %v", r)
	default:
	}
}
