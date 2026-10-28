package utp

import (
	"net"
	"testing"
	"time"
)

// Connections are keyed by the peer's address as a string (UDPPeer.Hash), so
// two renderings of the same address must give the same key and different
// addresses must not. On a dual-stack socket an address may arrive in its
// 16-byte v4-mapped form while the caller resolved a 4-byte one;
// net.IP.String renders both as a dotted quad, which this relies on.
func TestUDPPeerHashIdentifiesTheAddressNotItsEncoding(t *testing.T) {
	v4 := &net.UDPAddr{IP: net.IP{127, 0, 0, 1}, Port: 9000}
	v4in6 := &net.UDPAddr{IP: net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 127, 0, 0, 1}, Port: 9000}
	if a, b := NewUDPPeer(v4).Hash(), NewUDPPeer(v4in6).Hash(); a != b {
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
		h := NewUDPPeer(addr).Hash()
		// "2001:db8::1" and "2001:db8:0:0:0:0:0:1" are one address in two
		// spellings and must give the same key.
		if name == "v6 full address" {
			if want := NewUDPPeer(distinct["v6 other host"]).Hash(); h != want {
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

// An IPv6 peer keys a connection id as an IPv4 one does: the key combines
// send, recv and the peer, and an empty or unstable peer key would collapse
// every connection to the peer into one entry.
func TestConnectionIDHashDistinguishesIPv6Peers(t *testing.T) {
	peerA := NewUDPPeer(&net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881})
	peerB := NewUDPPeer(&net.UDPAddr{IP: net.ParseIP("2001:db8::2"), Port: 6881})

	same := NewConnectionID(peerA, 1, 2).Hash()
	if again := NewConnectionID(peerA, 1, 2).Hash(); again != same {
		t.Errorf("the same v6 connection id hashed two ways: %q then %q", same, again)
	}
	for name, other := range map[string]*ConnectionID{
		"other peer": NewConnectionID(peerB, 1, 2),
		"other recv": NewConnectionID(peerA, 3, 2),
		"other send": NewConnectionID(peerA, 1, 3),
	} {
		if other.Hash() == same {
			t.Errorf("%s hashes the same as the original connection id", name)
		}
	}
}

// A peer type this package did not create still receives packets: the
// address is parsed from its Hash. A Hash that is not an address is an
// error, not a silently dropped packet.
func TestUDPConnWritesToForeignPeerTypes(t *testing.T) {
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
	conn := &udpConn{base: sender}

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
// udpConn.WriteTo knows about.
type addressOnlyPeer string

func (p addressOnlyPeer) Hash() string { return string(p) }

var _ ConnectionPeer = addressOnlyPeer("")
