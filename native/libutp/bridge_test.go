//go:build cgo

package libutp

import (
	"bytes"
	"testing"
	"time"
)

// Validate the bridge before trusting any interop result. If libutp cannot
// talk to itself through this wrapper, an interop failure would say nothing
// about the Go implementation.

func TestBridgeLibutpToLibutp(t *testing.T) {
	server, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	client, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	t.Logf("server on :%d, client on :%d", server.Port(), client.Port())

	server.Listen()
	if err := client.Connect(server.Port()); err != nil {
		t.Fatal(err)
	}

	if _, err := client.WaitState(10*time.Second, StateConnected); err != nil {
		t.Fatalf("client never connected: %v", err)
	}
	if _, err := server.WaitState(10*time.Second, StateConnected); err != nil {
		t.Fatalf("server never accepted: %v", err)
	}

	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i * 7)
	}

	start := time.Now()
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got, err := server.ReadFull(len(payload), 30*time.Second)
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	elapsed := time.Since(start)

	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes", len(got))
	}
	t.Logf("libutp -> libutp: %d bytes in %v (%.1f Mbps)",
		len(got), elapsed.Round(time.Millisecond),
		float64(len(got))*8/elapsed.Seconds()/1e6)
}

func TestBridgePeerBindsAndReportsPort(t *testing.T) {
	p, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown()
	if p.Port() == 0 {
		t.Fatal("peer reported port 0 after binding to an ephemeral port")
	}
	if s := p.State(); s != StateIdle {
		t.Errorf("fresh peer state = %v, want idle", s)
	}
}

func TestBridgeShutdownIsIdempotent(t *testing.T) {
	p, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	p.Shutdown()
	p.Shutdown()
}

// A peer that is not listening must refuse an incoming connection, which is
// what the firewall callback is for.
func TestBridgeRefusesWhenNotListening(t *testing.T) {
	server, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	client, err := NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	// No Listen() call.
	if err := client.Connect(server.Port()); err != nil {
		t.Fatal(err)
	}
	if s := server.State(); s == StateConnected {
		t.Fatal("server accepted a connection without listening")
	}
	// The client should end up erroring rather than connecting.
	s, _ := client.WaitState(20*time.Second, StateConnected, StateError, StateDestroyed)
	t.Logf("client ended in state %v (err=%v)", s, client.Err())
	if s == StateConnected {
		t.Error("client connected to a peer that never listened")
	}
}
