package utpnet

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

type otherAddr string

func (a otherAddr) Network() string { return "other" }
func (a otherAddr) String() string  { return string(a) }

// The map from UDP form back to a PacketConn's own address type is bounded,
// however many addresses datagrams arrive from, and keeps the most recent.
// It forgot nothing, so a PacketConn carrying a DHT grew it for as long as
// it ran. From the release review of 9c44c4f (R10).
func TestNativeAddressesAreBounded(t *testing.T) {
	p := &packetConn{}
	var last otherAddr
	for i := 0; i < 3*nativeMax; i++ {
		last = otherAddr(fmt.Sprintf("10.%d.%d.%d:6881", byte(i>>16), byte(i>>8), byte(i)))
		if _, ok := p.udpForm(last); !ok {
			t.Fatalf("%v has no UDP form", last)
		}
	}
	if n := len(p.native); n > nativeMax {
		t.Fatalf("%d addresses remembered after %d, want at most %d", n, 3*nativeMax, nativeMax)
	}
	udp, _ := p.udpForm(last)
	if got := p.nativeOf(udp); got != last {
		t.Fatalf("the latest address maps back to %v, want %v", got, last)
	}
}

// An address a live connection uses is kept however many others arrive. The
// longest silent went first whatever they were, so a quiet connection's peer
// could be pushed out by a burst of new addresses, and the connection's
// writes then went to the UDP form, which a PacketConn of another address
// type need not accept. Review of 294cc2e, finding 4.
func TestNativeAddressOfALiveConnectionIsKept(t *testing.T) {
	p := &packetConn{}
	live := otherAddr("192.0.2.1:6881")
	udp, ok := p.udpForm(live)
	if !ok {
		t.Fatal("no UDP form")
	}
	p.pin(udp)
	for i := 0; i < 3*nativeMax; i++ {
		p.udpForm(otherAddr(fmt.Sprintf("10.%d.%d.%d:6881", byte(i>>16), byte(i>>8), byte(i))))
	}
	if got := p.nativeOf(udp); got != live {
		t.Fatalf("a pinned address maps back to %v (%T), want %v (%T)", got, got, live, live)
	}
	if n := len(p.native); n > nativeMax+1 {
		t.Fatalf("%d addresses remembered, want at most %d", n, nativeMax+1)
	}
	p.unpin(udp)
	for i := 0; i < 3*nativeMax; i++ {
		p.udpForm(otherAddr(fmt.Sprintf("10.%d.%d.%d:7000", byte(i>>16), byte(i>>8), byte(i))))
	}
	if got := p.nativeOf(udp); got == live {
		t.Fatal("an unpinned address was never pruned")
	}
}

// otherPacketConn is a UDP socket that names addresses as otherAddr and
// writes only to one.
type otherPacketConn struct{ *net.UDPConn }

func (c otherPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.UDPConn.ReadFrom(b)
	if addr != nil {
		return n, otherAddr(addr.String()), err
	}
	return n, nil, err
}

func (c otherPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	a, ok := addr.(otherAddr)
	if !ok {
		return 0, fmt.Errorf("otherPacketConn: cannot write to a %T", addr)
	}
	udp, err := net.ResolveUDPAddr("udp", string(a))
	if err != nil {
		return 0, err
	}
	return c.UDPConn.WriteTo(b, udp)
}

// A connection over such a PacketConn pins its peer's address for its life,
// and lets it go when it ends.
func TestAConnectionPinsItsPeersNativeAddress(t *testing.T) {
	open := func() *Socket {
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewSocketFromPacketConn(context.Background(), otherPacketConn{udp}, &Options{Logger: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	server, client := open(), open()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := server.Accept()
		accepted <- c
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialAddr(ctx, otherAddr(server.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	if peer == nil {
		t.FailNow()
	}
	pins := func(s *Socket) int {
		s.generic.mu.Lock()
		defer s.generic.mu.Unlock()
		return len(s.generic.pins)
	}
	if pins(client) != 1 || pins(server) != 1 {
		t.Fatalf("pins: client %d, server %d; want one each", pins(client), pins(server))
	}
	conn.Close()
	peer.Close()
	<-conn.(*Conn).Stream().Done()
	<-peer.(*Conn).Stream().Done()
	deadline := time.Now().Add(5 * time.Second)
	for pins(client)+pins(server) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("pins left after the connections ended: client %d, server %d", pins(client), pins(server))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
