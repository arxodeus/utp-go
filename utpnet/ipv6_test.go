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

// ipv6Transport is a way to get two Sockets on IPv6 addresses.
type ipv6Transport struct {
	name string
	pair func(t *testing.T) (server, client *Socket)
}

// ipv6Transports runs each IPv6 test twice: over the kernel's ::1, which
// skips on a host without IPv6, and over memNet, which always runs.
//
// The kernel run is the real thing and the only one that can catch a socket
// option or a dual-stack surprise. The memNet runs cover everything above
// the socket -- dialling by a v6 address string, keying connections by the
// peer's address, reporting addresses back -- on any host, including this
// one, whose kernel was built without IPv6. Before they existed every IPv6
// test here skipped on every run and IPv6 was rated "None" in
// COMPATIBILITY.md. The zoned link-local pair is the case most likely to
// break a key built from an address string.
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

// The addresses handed back over IPv6 have to be the real ones, in the
// bracketed form the rest of Go uses, or a client cannot tell peers apart.
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

// Two IPv6 peers that differ only in their address -- same port, or for
// link-local peers the same address and port on different interfaces -- are
// different connections, even when they happen to pick the same connection
// ids.
//
// A connection is keyed by its peer's address as a string together with its
// connection ids. The ids are random, so two peers usually differ there
// anyway, and a key that lost the v6 address or a link-local zone would go
// unnoticed -- the first version of this test, with random ids, passed with
// the address or the zone deliberately dropped from the key. Peers share ids
// by chance one pair in 65536, which a busy seeder meets; this gives both
// the same ids so that only the address separates them, and holds both
// connections open, since a collision only shows while the first occupies
// the key. In memory only: a kernel cannot give one host two addresses on
// ::1, and zones need real interfaces.
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

			// Each client sends its own address as its payload, so the
			// server can tell which stream reached which connection. Both
			// connections are held open until both have been read: a
			// collision on the key only shows while the first connection
			// still occupies it. With the connections closed as soon as they
			// finished, a key that lost the v6 address still passed -- the
			// second client's SYN was ignored as a duplicate, retransmitted,
			// and accepted three seconds later once the first had gone.
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
				// The same connection ids from both clients, and a deadline
				// shorter than a SYN retransmission (3 s). A server keying by a
				// collapsed address does not cross the two streams: it ignores
				// the second client's first SYN as belonging to the first
				// connection, and lets it in on the retransmission. Measured
				// with the v6 address dropped from the key: the second dial
				// took 3.03 s and held up the first connection's data with it,
				// where with a correct key both take milliseconds. A dial that
				// needs a retransmission is the failure.
				dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				cid := utp.NewConnectionId(utp.NewUdpPeer(serverAddr), 7000, 7001)
				stream, err := client.sock.ConnectWithCid(dialCtx, cid, client.config)
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
