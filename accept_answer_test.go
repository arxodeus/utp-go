package utp

import (
	"context"
	"net"
	"testing"
	"time"
)

func answerPair(t *testing.T, ctx context.Context, opts ...SocketOption) (dialer, acceptor *Socket) {
	t.Helper()
	lg := duplexQuietLog()
	var err error
	dialer, err = Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dialer.Close() })
	acceptor, err = Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { acceptor.Close() })
	return dialer, acceptor
}

func dialWithin(ctx context.Context, from, to *Socket, within time.Duration) (*Stream, error) {
	dctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	return from.Connect(dctx, NewUDPPeer(to.LocalAddr().(*net.UDPAddr)), NewConnectionConfig())
}

// With its own configuration, a socket answers a SYN as it arrives: the dial
// completes with nothing waiting in Accept, and the next Accept returns the
// connection, which carries data.
func TestSynIsAnsweredWithNoAcceptWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := NewConnectionConfig()
	cfg.WindowSize = 123_456
	dialer, acceptor := answerPair(t, ctx, WithAcceptConfig(func() *ConnectionConfig { return cfg }))

	start := time.Now()
	out, err := dialWithin(ctx, dialer, acceptor, 2*time.Second)
	if err != nil {
		t.Fatalf("dial with nothing waiting in Accept: %v", err)
	}
	defer out.Close()
	t.Logf("dial completed in %v with no Accept waiting", time.Since(start))
	if n := acceptor.AcceptBacklog(); n != 1 {
		t.Fatalf("AcceptBacklog = %d, want the one answered connection", n)
	}

	in, err := acceptor.Accept(ctx, nil)
	if err != nil {
		t.Fatalf("accepting the answered connection: %v", err)
	}
	defer in.Close()
	if got := in.WindowSize(); got != cfg.WindowSize {
		t.Fatalf("the answered connection has window ceiling %d, want the socket's %d", got, cfg.WindowSize)
	}
	if _, err := out.Write(ctx, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := in.Read(ctx, buf); err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
}

// Without one, the configuration comes from each Accept, so a SYN waits for
// an Accept: the dial does not complete until one comes, and then does.
func TestSynWaitsForAcceptWithoutASocketConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dialer, acceptor := answerPair(t, ctx)

	dialed := make(chan error, 1)
	go func() {
		s, err := dialWithin(ctx, dialer, acceptor, 15*time.Second)
		if err == nil {
			defer s.Close()
		}
		dialed <- err
	}()
	select {
	case err := <-dialed:
		t.Fatalf("the dial ended with no Accept and no socket configuration: %v", err)
	case <-time.After(time.Second):
	}
	in, err := acceptor.Accept(ctx, NewConnectionConfig())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer in.Close()
	if err := <-dialed; err != nil {
		t.Fatalf("the dial did not complete once an Accept came: %v", err)
	}
}

// AcceptWithCID takes the answered connection it names, leaving others for
// Accept.
func TestAcceptWithCIDTakesAnAnsweredConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dialer, acceptor := answerPair(t, ctx, WithAcceptConfig(NewConnectionConfig))

	first, err := dialWithin(ctx, dialer, acceptor, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := dialWithin(ctx, dialer, acceptor, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// The acceptor's ids for the second connection: it sends on the
	// dialer's receive id and receives on the dialer's send id.
	dcid := second.CID()
	want := NewConnectionID(NewUDPPeer(dialer.LocalAddr().(*net.UDPAddr)), dcid.Send, dcid.Recv)
	got, err := acceptor.AcceptWithCID(ctx, want, NewConnectionConfig())
	if err != nil {
		t.Fatalf("AcceptWithCID for an answered connection: %v", err)
	}
	defer got.Close()
	if got.CID().Hash() != want.Hash() {
		t.Fatalf("AcceptWithCID returned %v, want %v", got.CID(), want)
	}
	rest, err := acceptor.Accept(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rest.Close()
	if rest.CID().Hash() == want.Hash() {
		t.Fatal("Accept returned the connection AcceptWithCID already took")
	}
}
