package utp_go

import (
	"net"
	"testing"
	"time"
)

// Connections are keyed by the peer's address rendered as a string
// (UdpPeer.Hash, cid.go), so two renderings of the same address must produce
// the same key and two different addresses must not.
//
// The case that matters is a dual-stack socket. A caller resolving
// "127.0.0.1:9000" may get a 4-byte IP, while a packet arriving on a socket
// bound to "::" may be reported with the same address in its 16-byte
// v4-mapped form. If those hashed differently, the connection the dialler
// created and the connection the inbound packet looks up would be two
// different entries and nothing would ever match.
//
// net.IP.String renders a v4-mapped address as dotted quad, so they agree.
// That is load-bearing rather than incidental, which is why it is pinned here.
func TestUdpPeerHashIdentifiesTheAddressNotItsEncoding(t *testing.T) {
	v4 := &net.UDPAddr{IP: net.IP{127, 0, 0, 1}, Port: 9000}
	v4in6 := &net.UDPAddr{IP: net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 127, 0, 0, 1}, Port: 9000}
	if a, b := NewUdpPeer(v4).Hash(), NewUdpPeer(v4in6).Hash(); a != b {
		t.Errorf("the same address hashes two ways: 4-byte %q, v4-mapped %q; a dual-stack "+
			"socket would fail to match its own connections", a, b)
	}

	// And the distinctions that must survive.
	distinct := map[string]*net.UDPAddr{
		"v6 loopback":     {IP: net.IPv6loopback, Port: 9000},
		"v4 loopback":     {IP: net.IP{127, 0, 0, 1}, Port: 9000},
		"v6 other port":   {IP: net.IPv6loopback, Port: 9001},
		"v6 other host":   {IP: net.ParseIP("2001:db8::1"), Port: 9000},
		"v6 zoned":        {IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 9000},
		"v6 other zone":   {IP: net.ParseIP("fe80::1"), Zone: "eth1", Port: 9000},
		"v6 no zone":      {IP: net.ParseIP("fe80::1"), Port: 9000},
		"v6 unspecified":  {IP: net.IPv6zero, Port: 9000},
		"v6 full address": {IP: net.ParseIP("2001:db8:0:0:0:0:0:1"), Port: 9000},
	}
	seen := make(map[string]string, len(distinct))
	for name, addr := range distinct {
		h := NewUdpPeer(addr).Hash()
		// "2001:db8::1" and "2001:db8:0:0:0:0:0:1" are the same address in
		// two spellings, and must land on the same key.
		if name == "v6 full address" {
			if want := NewUdpPeer(distinct["v6 other host"]).Hash(); h != want {
				t.Errorf("%q hashed to %q, but the same address spelled short hashed to %q",
					name, h, want)
			}
			continue
		}
		if prev, ok := seen[h]; ok {
			t.Errorf("%q and %q both hash to %q; they are different peers", name, prev, h)
		}
		seen[h] = name
	}
}

// A v6 peer has to key a connection id the same way a v4 one does: the id
// hashes send, recv and the peer together (cid.go genHash), and a peer whose
// hash is empty or unstable would collapse every connection to one peer into
// a single entry.
func TestConnectionIdHashDistinguishesIPv6Peers(t *testing.T) {
	peerA := NewUdpPeer(&net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881})
	peerB := NewUdpPeer(&net.UDPAddr{IP: net.ParseIP("2001:db8::2"), Port: 6881})

	same := NewConnectionId(peerA, 1, 2).Hash()
	if again := NewConnectionId(peerA, 1, 2).Hash(); again != same {
		t.Errorf("the same v6 connection id hashed two ways: %q then %q", same, again)
	}
	for name, other := range map[string]*ConnectionId{
		"other peer": NewConnectionId(peerB, 1, 2),
		"other recv": NewConnectionId(peerA, 3, 2),
		"other send": NewConnectionId(peerA, 1, 3),
	} {
		if other.Hash() == same {
			t.Errorf("%s hashes the same as the original connection id", name)
		}
	}
}

// A peer type this package did not create must still receive packets.
//
// UdpConn.WriteTo switches on *UdpPeer and *ConnectionId and used to end in
// `return 0, nil` for anything else: the packet was dropped and the caller was
// told it had been sent. A connection built on such a peer would then time out
// with nothing anywhere to say why. Neither half of that is acceptable -- a
// send that did not happen is not a success -- so the address is parsed from
// the peer's Hash, which is the contract the rest of this repository already
// relies on (utpnet's peerUDPAddr), and a Hash that is not an address is an
// error rather than silence.
func TestUdpConnWritesToForeignPeerTypes(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	sender, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	conn := &UdpConn{base: sender}

	msg := []byte("to a peer type we did not define")
	n, err := conn.WriteTo(msg, addressOnlyPeer(listener.LocalAddr().String()))
	if err != nil {
		t.Fatalf("writing to a foreign peer type: %v", err)
	}
	if n != len(msg) {
		t.Errorf("wrote %d bytes, want %d", n, len(msg))
	}

	if err := listener.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	got, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("the packet never arrived: %v", err)
	}
	if string(buf[:got]) != string(msg) {
		t.Errorf("received %q, want %q", buf[:got], msg)
	}

	// And a peer whose hash is not an address is reported, not swallowed.
	if _, err := conn.WriteTo(msg, addressOnlyPeer("not an address")); err == nil {
		t.Error("writing to a peer whose hash is not an address reported success")
	}
}

// addressOnlyPeer implements ConnectionPeer without being any of the types
// UdpConn.WriteTo knows about.
type addressOnlyPeer string

func (p addressOnlyPeer) Hash() string { return string(p) }

var _ ConnectionPeer = addressOnlyPeer("")
