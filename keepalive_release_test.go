package utp

import (
	"context"
	"net"
	"testing"
	"time"
)

// A connection quiet for its keep-alive interval holds none of its receive
// buffer's storage, as libutp holds none of what it has delivered; one that
// is still busy keeps it.
func TestQuietConnectionReleasesReceiveStorage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lg := duplexQuietLog()
	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	cfg := NewConnectionConfig()
	cfg.KeepAliveInterval = 200 * time.Millisecond
	const cid = 4500
	accepted := make(chan *UtpStream, 1)
	go func() {
		s, _ := sb.AcceptWithCid(ctx, NewConnectionId(NewUdpPeer(sa.LocalAddr().(*net.UDPAddr)), cid+1, cid), cfg)
		accepted <- s
	}()
	client, err := sa.ConnectWithCid(ctx, NewConnectionId(NewUdpPeer(sb.LocalAddr().(*net.UDPAddr)), cid, cid+1), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	defer server.Close()

	if _, err := client.Write(ctx, make([]byte, 64*1024)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64*1024)
	for got := 0; got < 64*1024; {
		n, err := server.Read(ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		got += n
	}
	storage := func() int {
		server.conn.mu.Lock()
		defer server.conn.mu.Unlock()
		return len(server.conn.state.RecvBuf.buf)
	}
	if storage() == 0 {
		t.Fatal("the receiving end holds no storage right after 64 KB; nothing to release")
	}
	deadline := time.Now().Add(5 * time.Second)
	for storage() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("quiet for %v past its keep-alive interval, the receiving end still holds %d bytes",
				5*time.Second, storage())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
