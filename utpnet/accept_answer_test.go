package utpnet

import (
	"context"
	"testing"
	"time"
)

// A Socket answers a SYN as it arrives, as libutp does: a dial completes
// with nothing waiting in Accept, and the next Accept returns it. Before,
// the dial waited for an Accept and failed after 21 s if none came.
func TestDialCompletesWithNoAcceptWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := Listen(ctx, "udp", "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Listen(ctx, "udp", "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
	defer dcancel()
	out, err := a.DialContext(dctx, "udp", b.Addr().String())
	if err != nil {
		t.Fatalf("dial with nothing waiting in Accept: %v", err)
	}
	defer out.Close()
	if n := b.AcceptBacklog(); n != 1 {
		t.Fatalf("AcceptBacklog = %d, want 1", n)
	}
	in, err := b.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if in.RemoteAddr().String() != a.Addr().String() {
		t.Fatalf("accepted a connection from %v, want %v", in.RemoteAddr(), a.Addr())
	}
}
