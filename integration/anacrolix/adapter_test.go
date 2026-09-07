package anacrolix

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/zen-eth/utp-go/utpnet"
)

// The check this module exists for, made against torrent's own interface
// rather than against a copy of it.
//
// torrent's `utpSocket` is unexported, so it cannot be named from here. But
// torrent.NewUtpSocket returns it, and reflection can reach the type through
// that function's signature. Comparing against the real thing means this test
// fails if torrent changes the interface -- which a hand-copied interface
// declaration would not.
func TestSatisfiesTorrentsUtpSocketInterface(t *testing.T) {
	fnType := reflect.TypeOf(torrent.NewUtpSocket)
	if fnType.NumOut() != 2 {
		t.Fatalf("torrent.NewUtpSocket returns %d values, expected (utpSocket, error); "+
			"this test needs updating", fnType.NumOut())
	}
	required := fnType.Out(0)
	if required.Kind() != reflect.Interface {
		t.Fatalf("torrent.NewUtpSocket's first return is %v, not an interface", required)
	}

	ours := reflect.TypeOf((*utpnet.Socket)(nil))
	if !ours.Implements(required) {
		var missing []string
		for i := 0; i < required.NumMethod(); i++ {
			m := required.Method(i)
			if _, ok := ours.MethodByName(m.Name); !ok {
				missing = append(missing, fmt.Sprintf("%s%v", m.Name, m.Type))
			}
		}
		t.Fatalf("*utpnet.Socket does not satisfy torrent's uTP socket interface (%v).\n"+
			"Missing or mismatched: %v", required, missing)
	}

	t.Logf("torrent requires %d methods; *utpnet.Socket has all of them", required.NumMethod())
	for i := 0; i < required.NumMethod(); i++ {
		m := required.Method(i)
		t.Logf("  %s%v", m.Name, m.Type)
	}
}

// And that the constructor this package offers really produces one.
func TestNewUtpSocket(t *testing.T) {
	sock, err := NewUtpSocket("utp", "127.0.0.1:0", nil, alog.Default)
	if err != nil {
		t.Fatalf("creating a socket: %v", err)
	}
	defer sock.Close()

	if sock.Addr() == nil {
		t.Error("socket has no address")
	}
	if sock.LocalAddr() == nil {
		t.Error("socket has no local address")
	}
	t.Logf("listening on %v", sock.Addr())
}

// A BitTorrent-shaped workload over the adapter: many concurrent peer
// connections, each exchanging messages in both directions, over one UDP port
// on each side.
//
// This is not a torrent transfer -- torrent selects its uTP implementation at
// build time, so it cannot be handed one at run time without patching that
// package (see README.md for the recipe). What it does test is the traffic
// shape a torrent client produces, which is what would break first: many
// simultaneous streams multiplexed on a shared port, each bidirectional and
// short-lived.
func TestConcurrentPeerConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}

	const peers = 24
	const messageSize = 16 * 1024

	server, err := utpnet.Listen(context.Background(), "udp", "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client, err := utpnet.Listen(context.Background(), "udp", "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Each accepted connection echoes what it is sent, as a peer answering a
	// request does.
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for i := 0; i < peers; i++ {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, io.LimitReader(c, messageSize))
			}(conn)
		}
	}()

	payload := make([]byte, messageSize)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, peers)
	for i := 0; i < peers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			conn, err := client.DialContext(ctx, "udp", server.Addr().String())
			if err != nil {
				errs <- fmt.Errorf("peer %d dial: %w", n, err)
				return
			}
			defer conn.Close()

			writeErr := make(chan error, 1)
			go func() {
				_, err := conn.Write(payload)
				writeErr <- err
			}()

			got := make([]byte, messageSize)
			if _, err := io.ReadFull(conn, got); err != nil {
				errs <- fmt.Errorf("peer %d read back: %w", n, err)
				return
			}
			if err := <-writeErr; err != nil {
				errs <- fmt.Errorf("peer %d write: %w", n, err)
				return
			}
			for j := range got {
				if got[j] != payload[j] {
					errs <- fmt.Errorf("peer %d: echoed data differs at byte %d", n, j)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	failures := 0
	for err := range errs {
		t.Error(err)
		failures++
	}
	if failures == 0 {
		t.Logf("%d concurrent peer connections, %d KiB echoed each way on each, over one port per side",
			peers, messageSize/1024)
	}
	<-acceptDone
}
