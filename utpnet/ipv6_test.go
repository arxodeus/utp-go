package utpnet

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// requireIPv6Loopback skips the test when the host has no IPv6 stack, which
// is a property of the machine rather than of this package. The skip message
// says so, so a run that never exercised IPv6 is not mistaken for one that
// did.
func requireIPv6Loopback(t *testing.T) {
	t.Helper()
	c, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this host (%v); this test measures nothing here", err)
	}
	c.Close()
}

// ipv6Transport is a way to get two Sockets on IPv6 addresses.
type ipv6Transport struct {
	name string
	pair func(t *testing.T) (server, client *Socket)
}

// ipv6Transports runs each IPv6 test twice: over the kernel's ::1, which
// skips on a host without IPv6, and over memNet, which always runs. The
// kernel run can catch socket-option and dual-stack issues; the memNet runs
// cover everything above the socket on any host, including a zoned
// link-local pair, the case most likely to break a key built from an address
// string.
func ipv6Transports() []ipv6Transport {
	return []ipv6Transport{
		{"kernel ::1", func(t *testing.T) (*Socket, *Socket) {
			requireIPv6Loopback(t)
			return listenPairIPv6(t)
		}},
		{"in-memory 2001:db8::", func(t *testing.T) (*Socket, *Socket) {
			return memPairIPv6(t, "[2001:db8::1]:6881", "[2001:db8::2]:51413")
		}},
		{"in-memory fe80:: with zone", func(t *testing.T) (*Socket, *Socket) {
			return memPairIPv6(t, "[fe80::1%eth0]:6881", "[fe80::2%eth0]:51413")
		}},
	}
}

func memPairIPv6(t *testing.T, serverAddr, clientAddr string) (server, client *Socket) {
	t.Helper()
	network := newMemNet()
	opts := &Options{Logger: quiet()}
	for _, side := range []struct {
		addr string
		sock **Socket
	}{{serverAddr, &server}, {clientAddr, &client}} {
		conn, err := network.listen(side.addr)
		if err != nil {
			t.Fatalf("binding %s: %v", side.addr, err)
		}
		s, err := newSocket(context.Background(), conn, opts)
		if err != nil {
			t.Fatalf("socket on %s: %v", side.addr, err)
		}
		t.Cleanup(func() { s.Close() })
		*side.sock = s
	}
	return server, client
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

// A transfer over IPv6, end to end. The protocol is address-family
// independent, but connections are keyed by the peer's address as a string,
// so the dialer's rendering of an address and the kernel's rendering of the
// same address must agree.
func TestDialAcceptTransferIPv6(t *testing.T) {
	for _, tr := range ipv6Transports() {
		tr := tr
		t.Run(tr.name, func(t *testing.T) {
			server, client := tr.pair(t)

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
		})
	}
}

// The addresses handed back over IPv6 are the real ones, in the bracketed
// form the rest of Go uses.
func TestConnAddressesIPv6(t *testing.T) {
	for _, tr := range ipv6Transports() {
		tr := tr
		t.Run(tr.name, func(t *testing.T) {
			server, client := tr.pair(t)

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
		})
	}
}

// The address conversions a v6 address passes through in this package, which
// need no IPv6 stack. Both are string-based, where v6 addresses go wrong: an
// unbracketed "::1:8080" is ambiguous and a bracketed one is not.
func TestIPv6AddressPlumbing(t *testing.T) {
	// A "utp6" network must not lose its "6", or a v6 dial resolves as v4
	// and fails.
	for _, tc := range []struct{ in, want string }{
		{"utp6", "udp6"}, {"udp6", "udp6"}, {"utp4", "udp4"}, {"udp4", "udp4"},
		{"utp", "udp"}, {"udp", "udp"}, {"", "udp"},
	} {
		if got := udpNetwork(tc.in); got != tc.want {
			t.Errorf("udpNetwork(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// peerUDPAddr's fallback parses a peer's Hash, which is the address by
	// contract. For a v6 peer the hash is bracketed and must survive the
	// round trip unchanged.
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

// hashOnlyPeer is a caller-supplied peer type: not *utp.UDPPeer, so
// peerUDPAddr takes its parsing fallback.
type hashOnlyPeer string

func (p hashOnlyPeer) Hash() string { return string(p) }

var _ utp.ConnectionPeer = hashOnlyPeer("")

// Two IPv6 peers that differ only in their address (same port, or for
// link-local peers the same address and port on different interfaces) are
// different connections, even with the same connection ids. Both clients use
// the same ids, so only the address separates them, and both connections are
// held open, since a key collision only shows while the first occupies the
// key. In memory only: a kernel cannot give one host two addresses on ::1,
// and zones need real interfaces.
func TestIPv6PeersDifferingOnlyInAddressAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name    string
		server  string
		clients [2]string
	}{
		{"same port, different address", "[2001:db8::1]:6881",
			[2]string{"[2001:db8::2]:4000", "[2001:db8::3]:4000"}},
		{"same link-local address and port, different zone", "[fe80::1%eth0]:6881",
			[2]string{"[fe80::2%eth0]:4000", "[fe80::2%eth1]:4000"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			network := newMemNet()
			opts := &Options{Logger: quiet()}
			open := func(addr string) *Socket {
				conn, err := network.listen(addr)
				if err != nil {
					t.Fatalf("binding %s: %v", addr, err)
				}
				s, err := newSocket(context.Background(), conn, opts)
				if err != nil {
					t.Fatalf("socket on %s: %v", addr, err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
			server := open(tc.server)

			// Each client sends its own address as its payload, so the server can
			// tell which stream reached which connection. Both connections are
			// held open until both have been read.
			const msgLen = 64
			pad := func(s string) []byte {
				b := make([]byte, msgLen)
				copy(b, s)
				return b
			}
			type got struct{ remote, body string }
			results := make(chan got, 2)
			hold := make(chan struct{})
			defer close(hold)
			go func() {
				for i := 0; i < 2; i++ {
					conn, err := server.Accept()
					if err != nil {
						return
					}
					go func(c net.Conn) {
						defer c.Close()
						body := make([]byte, msgLen)
						if _, err := io.ReadFull(c, body); err != nil {
							return
						}
						results <- got{c.RemoteAddr().String(), string(bytes.TrimRight(body, "\x00"))}
						<-hold
					}(conn)
				}
			}()

			serverAddr, err := net.ResolveUDPAddr("udp6", tc.server)
			if err != nil {
				t.Fatal(err)
			}
			for _, addr := range tc.clients {
				client := open(addr)
				// The same connection ids from both clients, and a deadline shorter
				// than a SYN retransmission (3 s). A server keying by a collapsed
				// address would ignore the second client's first SYN and admit it
				// only on the retransmission, so a dial that needs one is the
				// failure.
				dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				cid := utp.NewConnectionID(utp.NewUDPPeer(serverAddr), 7000, 7001)
				stream, err := client.sock.ConnectWithCID(dialCtx, cid, client.config.Load())
				cancel()
				if err != nil {
					t.Fatalf("dialling from %s while the other peer's connection is open: %v", addr, err)
				}
				conn := newConn(client, stream)
				t.Cleanup(func() { conn.Close() })
				if _, err := conn.Write(pad(client.Addr().String())); err != nil {
					t.Fatalf("writing from %s: %v", addr, err)
				}
			}

			seen := map[string]bool{}
			for i := 0; i < 2; i++ {
				select {
				case r := <-results:
					if r.body != r.remote {
						t.Errorf("the connection from %s received %q: two peers' streams were crossed",
							r.remote, r.body)
					}
					seen[r.remote] = true
				case <-time.After(10 * time.Second):
					t.Fatalf("only %d of 2 connections delivered", i)
				}
			}
			if len(seen) != 2 {
				t.Errorf("two peers arrived as %d distinct connections: %v", len(seen), seen)
			}
		})
	}
}

// A transfer to an IPv6 peer over a path at IPv6's minimum link MTU, 1280,
// whose routers' "packet too big" messages never arrive. Every IPv6 path
// carries 1280 and tunnels often little more, so an IPv6 peer starts at a
// 1232-byte ceiling (ipv6Ceiling).
func TestIPv6TransferOverTheMinimumMTUPath(t *testing.T) {
	network := newMemNet()
	network.mtu = 1280 - 48 // the UDP payload a 1280-byte IPv6 link carries
	opts := &Options{Logger: quiet()}
	open := func(addr string) *Socket {
		conn, err := network.listen(addr)
		if err != nil {
			t.Fatalf("binding %s: %v", addr, err)
		}
		s, err := newSocket(context.Background(), conn, opts)
		if err != nil {
			t.Fatalf("socket on %s: %v", addr, err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	server := open("[2001:db8::1]:6881")
	client := open("[2001:db8::2]:51413")

	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	received := make(chan []byte, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		got, _ := io.ReadAll(conn)
		received <- got
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp6", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	go func() {
		_, _ = conn.Write(payload)
		conn.Close()
	}()

	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("delivered %d of %d bytes, or corrupted", len(got), len(payload))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stalled: a 1 MB transfer to an IPv6 peer did not complete over a 1280-byte path")
	}
}
