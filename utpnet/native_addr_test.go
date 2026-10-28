package utpnet

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type otherAddr string

func (a otherAddr) Network() string { return "other" }
func (a otherAddr) String() string  { return string(a) }

// The map from UDP form back to a PacketConn's own address type is bounded
// however many addresses datagrams arrive from, and keeps the most recent.
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

// An address a live connection uses is kept however many others arrive, so
// a quiet connection's peer is not pushed out by a burst of new addresses.
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

// firstLost is otherPacketConn losing the first datagram it is asked to send,
// and saying so.
type firstLost struct {
	otherPacketConn
	once *sync.Once
	lost chan struct{}
}

func (c firstLost) WriteTo(b []byte, addr net.Addr) (int, error) {
	dropped := false
	c.once.Do(func() { dropped = true; close(c.lost) })
	if dropped {
		return len(b), nil
	}
	return c.otherPacketConn.WriteTo(b, addr)
}

// churn has p learn more than it can remember of other addresses, as a busy
// port would.
func churn(p *packetConn) {
	for i := 0; i < 2*nativeMax; i++ {
		p.udpForm(otherAddr(fmt.Sprintf("10.%d.%d.%d:6881", i>>16&0xff, i>>8&0xff, i&0xff)))
	}
}

func otherSocket(t *testing.T, wrap func(otherPacketConn) net.PacketConn) *Socket {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	var pc net.PacketConn = otherPacketConn{udp}
	if wrap != nil {
		pc = wrap(otherPacketConn{udp})
	}
	s, err := NewSocketFromPacketConn(context.Background(), pc, &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A dial keeps its peer's native address through the handshake, so a SYN
// resent after other addresses have crowded the map still goes to the
// native address.
func TestADialKeepsItsPeersNativeAddressThroughTheHandshake(t *testing.T) {
	server := otherSocket(t, nil)
	lost := make(chan struct{})
	client := otherSocket(t, func(c otherPacketConn) net.PacketConn {
		return firstLost{otherPacketConn: c, once: new(sync.Once), lost: lost}
	})
	go func() {
		if c, err := server.Accept(); err == nil {
			c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dialled := make(chan error, 1)
	go func() {
		c, err := client.DialAddr(ctx, otherAddr(server.LocalAddr().String()))
		if err == nil {
			c.Close()
		}
		dialled <- err
	}()
	<-lost
	churn(client.generic)
	if err := <-dialled; err != nil {
		t.Fatalf("the dial failed after its first SYN was lost and other addresses arrived: %v", err)
	}
}

// A connection waiting to be accepted keeps its peer's native address.
func TestAConnectionWaitingToBeAcceptedKeepsItsPeersNativeAddress(t *testing.T) {
	server, client := otherSocket(t, nil), otherSocket(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialAddr(ctx, otherAddr(server.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for server.AcceptBacklog() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the dialled connection never reached the backlog")
		}
		time.Sleep(time.Millisecond)
	}

	churn(server.generic)
	peer, err := server.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := peer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("the accepted connection's data never arrived: %v", err)
	}
}

// A uTP datagram keeps the address it came from while it waits to be
// handled. Handed to the uTP socket, its address is registered again and
// held until the socket reads the next datagram.
func TestAQueuedDatagramKeepsItsNativeAddress(t *testing.T) {
	p := &packetConn{}
	d := newDemuxConn(p)
	native := otherAddr("192.0.2.7:6881")
	udp, ok := p.udpForm(native)
	if !ok {
		t.Fatal("no UDP form")
	}
	d.deliver([]byte("syn"), nil, udp, native)

	churn(p) // while it waits
	if got := p.nativeOf(udp); got != net.Addr(udp) {
		t.Fatalf("the churn left %v remembered; this test needs it forgotten", got)
	}

	if _, _, err := d.ReadFrom(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	if got := p.nativeOf(udp); got != net.Addr(native) {
		t.Fatalf("handed to the uTP socket, the datagram's address is %v (%T), want %v", got, got, native)
	}
	churn(p) // while the socket handles it
	if got := p.nativeOf(udp); got != net.Addr(native) {
		t.Fatalf("forgotten while the uTP socket was handling its datagram: %v", got)
	}

	// The next read lets it go.
	d.deliver([]byte("next"), nil, &net.UDPAddr{IP: net.IPv4(192, 0, 2, 8), Port: 1}, nil)
	if _, _, err := d.ReadFrom(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	pins := len(p.pins)
	p.mu.Unlock()
	if pins != 0 {
		t.Fatalf("%d addresses still held after the next read", pins)
	}
}

// A datagram for ReadFrom keeps the address it came from in the PacketConn's
// own form, however busy the port is while it waits.
func TestAPassthroughDatagramKeepsItsNativeAddress(t *testing.T) {
	s := otherSocket(t, nil)
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.WriteTo([]byte("dht"), s.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.passthrough) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the datagram never reached the passthrough queue")
		}
		time.Sleep(time.Millisecond)
	}
	churn(s.generic)
	s.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, from, err := s.ReadFrom(make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := from.(otherAddr); !ok {
		t.Fatalf("ReadFrom gave the sender as a %T, want the PacketConn's own otherAddr", from)
	}
}
