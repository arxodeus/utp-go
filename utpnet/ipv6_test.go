package utpnet

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// requireIPv6Loopback skips the test when the host has no IPv6 stack.
//
// It is a skip rather than a failure because address family availability is a
// property of the machine, not of this library: a container started without
// IPv6 answers "address family not supported by protocol" to the listen below,
// and nothing in this package can change that. The skip message says so, so a
// run that never exercised IPv6 cannot be mistaken for one that did.
func requireIPv6Loopback(t *testing.T) {
	t.Helper()
	c, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this host (%v); this test measures nothing here", err)
	}
	c.Close()
}

func listenPairIPv6(t *testing.T) (server, client *Socket) {
	t.Helper()
	opts := &Options{Logger: quiet()}
	server, err := Listen(context.Background(), "udp6", "[::1]:0", opts)
	if err != nil {
		t.Fatalf("listening server: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	client, err = Listen(context.Background(), "udp6", "[::1]:0", opts)
	if err != nil {
		t.Fatalf("listening client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client
}

// A transfer over IPv6, end to end.
//
// Nothing in the protocol is address-family dependent -- a uTP header carries
// no addresses, and the path-MTU ceiling is a UDP payload size (1400), which
// clears a 1280-byte IPv6 minimum link MTU with its 40-byte header. What can
// break is the plumbing around it: connections are keyed by the peer's address
// as a string, so the dialler's rendering of an address and the kernel's
// rendering of the same address's packets have to agree, and that is a
// different code path for a v6 address than for a v4 one.
func TestDialAcceptTransferIPv6(t *testing.T) {
	requireIPv6Loopback(t)
	server, client := listenPairIPv6(t)

	payload := make([]byte, 256*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	received := make(chan []byte, 1)
	errs := make(chan error, 2)

	go func() {
		conn, err := server.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		got, err := io.ReadAll(conn)
		if err != nil {
			errs <- err
			return
		}
		received <- got
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp6", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling over IPv6: %v", err)
	}

	go func() {
		if _, err := conn.Write(payload); err != nil {
			errs <- err
			return
		}
		conn.Close()
	}()

	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload corrupted over IPv6: got %d bytes, sent %d", len(got), len(payload))
		}
	case err := <-errs:
		t.Fatalf("IPv6 transfer failed: %v", err)
	case <-time.After(90 * time.Second):
		t.Fatal("IPv6 transfer did not complete")
	}
}

// The addresses handed back over IPv6 have to be the real ones, in the
// bracketed form the rest of Go uses, or a client cannot tell peers apart.
func TestConnAddressesIPv6(t *testing.T) {
	requireIPv6Loopback(t)
	server, client := listenPairIPv6(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp6", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling over IPv6: %v", err)
	}
	defer conn.Close()

	if got := conn.RemoteAddr().String(); got != server.Addr().String() {
		t.Errorf("RemoteAddr is %q, want the server's address %q", got, server.Addr())
	}
	select {
	case in := <-accepted:
		defer in.Close()
		if got := in.RemoteAddr().String(); got != client.Addr().String() {
			t.Errorf("accepted RemoteAddr is %q, want the dialler's address %q", got, client.Addr())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no IPv6 connection was accepted")
	}
}

// The address plumbing itself, which needs no IPv6 stack to exercise.
//
// These are the two conversions a v6 address passes through in this package,
// and both are string-shaped, which is where v6 addresses go wrong: an
// unbracketed "::1:8080" is ambiguous and a bracketed one is not.
func TestIPv6AddressPlumbing(t *testing.T) {
	// anacrolix/torrent names its networks "utp", "utp4" and "utp6"; a "6"
	// must not be lost, or a v6 dial resolves as v4 and fails.
	for _, tc := range []struct{ in, want string }{
		{"utp6", "udp6"}, {"udp6", "udp6"}, {"utp4", "udp4"}, {"udp4", "udp4"},
		{"utp", "udp"}, {"udp", "udp"}, {"", "udp"},
	} {
		if got := udpNetwork(tc.in); got != tc.want {
			t.Errorf("udpNetwork(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// peerUDPAddr's fallback re-parses a peer's Hash, which is by contract
	// the address. For a v6 peer that hash is bracketed, and has to survive
	// the round trip unchanged.
	for _, addr := range []*net.UDPAddr{
		{IP: net.IPv6loopback, Port: 8080},
		{IP: net.ParseIP("2001:db8::1"), Port: 1},
		{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 6881},
	} {
		peer := hashOnlyPeer(addr.String())
		got, err := peerUDPAddr(peer)
		if err != nil {
			t.Errorf("peerUDPAddr(%q): %v", addr, err)
			continue
		}
		if got.String() != addr.String() {
			t.Errorf("peerUDPAddr(%q) came back as %q", addr, got)
		}
	}
}

// hashOnlyPeer is a caller-supplied peer type: not *utp.UdpPeer, so
// peerUDPAddr takes its parsing fallback.
type hashOnlyPeer string

func (p hashOnlyPeer) Hash() string { return string(p) }

var _ utp.ConnectionPeer = hashOnlyPeer("")
