package utpnet

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// The firewall callback: refuse a peer before any state exists for it.
//
// libutp asks its embedder about every SYN for a connection it does not
// already have, after the duplicate check and before it creates the socket:
//
//	// true means yes, block connection.  false means no, don't block.
//	if (utp_call_on_firewall(ctx, to, tolen)) {
//	    ...
//	    return 1;
//	}
//	                                        (utp_internal.cpp:2975-2982)
//
// This library accepted a callback at the anacrolix adapter and ignored it,
// which is the worst of the three options available: a caller that passed a
// blocklist got no blocking, and no sign that it was not happening.
//
// Two properties matter beyond "the connection does not happen". A refusal
// must create no connection state, or a blocklist becomes a way to make a node
// allocate; and it must be silent, because answering -- with a RESET or
// anything else -- confirms to a refused peer that something is listening
// here, which is the opposite of what a blocklist is for. libutp's refusal is
// a bare `return 1`.
func TestFirewallRefusesBeforeAnyStateExists(t *testing.T) {
	var (
		mu    sync.Mutex
		asked []string
	)
	blocked := make(chan string, 4)

	server, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{
		Logger: quiet(),
		Firewall: func(addr net.Addr) bool {
			mu.Lock()
			asked = append(asked, addr.String())
			mu.Unlock()
			// Refuse everyone, so the test does not depend on which port the
			// kernel handed the dialler.
			select {
			case blocked <- addr.String():
			default:
			}
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("the dial succeeded against a socket whose firewall refuses everything")
	}

	select {
	case from := <-blocked:
		if from != client.Addr().String() {
			t.Errorf("the firewall was asked about %q, want the dialler's address %q",
				from, client.Addr())
		}
	default:
		t.Fatal("the firewall was never consulted; the callback is being ignored again")
	}

	select {
	case <-accepted:
		t.Error("a refused connection was handed to Accept")
	default:
	}

	// No state, and no answer.
	if n := server.sock.NumConnections(); n != 0 {
		t.Errorf("the refusing socket is tracking %d connection(s); a refusal must not "+
			"allocate, or a blocklist becomes a way to make a node allocate", n)
	}
	if n := server.sock.PacketsResetSent(); n != 0 {
		t.Errorf("the refusing socket sent %d RESET(s); libutp refuses with a bare return "+
			"(utp_internal.cpp:2981), because answering tells a refused peer that something "+
			"is listening", n)
	}
	if n := server.sock.ConnectionsRefusedByFirewall(); n == 0 {
		t.Error("the socket counted no refusals")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(asked) == 0 {
		t.Fatal("the firewall recorded no calls")
	}
}

// A firewall that admits a peer must not get in its way.
func TestFirewallAdmitsWhatItDoesNotRefuse(t *testing.T) {
	var consulted int
	var mu sync.Mutex

	server, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{
		Logger: quiet(),
		Firewall: func(net.Addr) bool {
			mu.Lock()
			consulted++
			mu.Unlock()
			return false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	const payload = "through the firewall"
	got := make(chan string, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b, err := io.ReadAll(conn)
		if err == nil {
			got <- string(b)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling through an admitting firewall: %v", err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	select {
	case s := <-got:
		if s != payload {
			t.Fatalf("received %q, want %q", s, payload)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the admitted connection never delivered anything")
	}

	mu.Lock()
	defer mu.Unlock()
	if consulted == 0 {
		t.Error("the firewall was never consulted for an admitted connection")
	}
	if n := server.sock.ConnectionsRefusedByFirewall(); n != 0 {
		t.Errorf("the socket counted %d refusals for a firewall that refuses nothing", n)
	}
}
