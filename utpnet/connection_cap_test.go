package utpnet

import (
	"context"
	"net"
	"testing"
	"time"
)

// Options.MaxConnections reaches the socket, over real UDP.
//
// A cap of 1 admits two connections -- the check is libutp's "more than", see
// utp.WithMaxConnections -- and refuses the third, silently: its dial times
// out, nothing is accepted, and no RESET is sent.
func TestMaxConnectionsRefusesPastTheCap(t *testing.T) {
	server, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{
		Logger:         quiet(),
		MaxConnections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	accepted := make(chan net.Conn, 3)
	go func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()

	dial := func(timeout time.Duration) (net.Conn, error) {
		client, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return client.DialContext(ctx, "udp", server.Addr().String())
	}

	for i := 1; i <= 2; i++ {
		conn, err := dial(5 * time.Second)
		if err != nil {
			t.Fatalf("dial %d, under the cap: %v", i, err)
		}
		defer conn.Close()
		select {
		case c := <-accepted:
			defer c.Close()
		case <-time.After(5 * time.Second):
			t.Fatalf("connection %d was dialled but never accepted", i)
		}
	}

	if conn, err := dial(2 * time.Second); err == nil {
		conn.Close()
		t.Fatal("a third connection was established past a cap of 1")
	}
	select {
	case <-accepted:
		t.Error("a refused connection was handed to Accept")
	default:
	}
	if n := server.sock.ConnectionsRefusedAtCapacity(); n == 0 {
		t.Error("the socket counted no refusals at capacity")
	}
	if n := server.sock.PacketsResetSent(); n != 0 {
		t.Errorf("the socket sent %d RESET(s) at capacity; libutp refuses with a bare "+
			"`return 1` (utp_internal.cpp:2973)", n)
	}
}
