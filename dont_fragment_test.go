package utp_go

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// dfConn is a Conn that records how each datagram was asked to be sent.
type dfConn struct {
	mu sync.Mutex
	// sends records one entry per datagram: true if it was sent through
	// WriteToDontFragment, false if through the ordinary WriteTo.
	sends []bool
	sizes []int
	// unsupported makes WriteToDontFragment refuse, as a platform with no
	// such socket option does.
	unsupported bool
	// omitInterface drops the DontFragmentWriter implementation entirely.
	omitInterface bool
}

func (c *dfConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	select {} // never reads; these tests only look at the write side
}

func (c *dfConn) WriteTo(b []byte, dst ConnectionPeer) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, false)
	c.sizes = append(c.sizes, len(b))
	return len(b), nil
}

func (c *dfConn) WriteToDontFragment(b []byte, dst ConnectionPeer) (int, error) {
	if c.unsupported {
		return 0, ErrDontFragmentUnsupported
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, true)
	c.sizes = append(c.sizes, len(b))
	return len(b), nil
}

func (c *dfConn) Close() error { return nil }

func (c *dfConn) taken() ([]bool, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bool(nil), c.sends...), append([]int(nil), c.sizes...)
}

func newDFSocket(t *testing.T, conn Conn) (*UtpSocket, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	return WithSocket(ctx, conn, conformanceLogger()), cancel
}

// The bit reaches a Conn that can honour it, and only for a probe.
func TestDontFragmentReachesTheConn(t *testing.T) {
	conn := &dfConn{}
	s, cancel := newDFSocket(t, conn)
	defer cancel()
	defer s.Close()

	peer := &UdpPeer{}
	if _, err := s.writeDatagram([]byte("probe"), peer, true); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if _, err := s.writeDatagram([]byte("ordinary"), peer, false); err != nil {
		t.Fatalf("ordinary: %v", err)
	}

	sends, _ := conn.taken()
	want := []bool{true, false}
	if len(sends) != len(want) {
		t.Fatalf("expected %d datagrams, got %d", len(want), len(sends))
	}
	for i := range want {
		if sends[i] != want[i] {
			t.Errorf("datagram %d: don't-fragment was %v, expected %v", i, sends[i], want[i])
		}
	}
}

// A Conn that implements the interface but cannot honour it right now must
// still get the datagram. Losing the probe instead would turn a missing socket
// option into a hole in the sequence space.
func TestDontFragmentUnsupportedStillSends(t *testing.T) {
	conn := &dfConn{unsupported: true}
	s, cancel := newDFSocket(t, conn)
	defer cancel()
	defer s.Close()

	n, err := s.writeDatagram([]byte("probe"), &UdpPeer{}, true)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if n != len("probe") {
		t.Errorf("sent %d bytes, expected %d", n, len("probe"))
	}
	sends, sizes := conn.taken()
	if len(sends) != 1 || sends[0] {
		t.Fatalf("expected exactly one ordinary send, got %v", sends)
	}
	if sizes[0] != len("probe") {
		t.Errorf("the fallback sent %d bytes, expected %d", sizes[0], len("probe"))
	}
}

// A Conn that never heard of the interface is unaffected.
func TestDontFragmentConnWithoutTheInterface(t *testing.T) {
	inner := &dfConn{}
	// path_mtu_test.go's plainConn embeds the Conn *interface*, so only
	// ReadFrom, WriteTo and Close are promoted: the DontFragmentWriter method
	// on the value inside is invisible to a type assertion. That is precisely
	// the Conn this case is about -- the libutp driver, an emulated network,
	// anything written before this interface existed.
	s, cancel := newDFSocket(t, &plainConn{Conn: inner})
	defer cancel()
	defer s.Close()

	if _, err := s.writeDatagram([]byte("probe"), &UdpPeer{}, true); err != nil {
		t.Fatalf("probe: %v", err)
	}
	sends, _ := inner.taken()
	if len(sends) != 1 || sends[0] {
		t.Fatalf("expected one ordinary send, got %v", sends)
	}
}

// The hop between the connection and the socket: a packet emitted as a probe
// must carry the bit on its socket event, and an ordinary one must not.
//
// The tests above exercise the last hop, from the write loop to the Conn.
// This is the one before it, and between them they cover the whole path from
// mtuSearch.eligibleProbe to WriteToDontFragment.
func TestDontFragmentTravelsOnTheSocketEvent(t *testing.T) {
	events := make(chan *socketEvent, 2)
	c := &connection{socketEvents: events}

	pkt := NewPacketBuilder(st_data, 1, 0, 1024, 1).WithPayload([]byte("x")).Build()
	c.emitPacket(pkt, true)
	c.emitPacket(pkt, false)

	probe := <-events
	ordinary := <-events
	if !probe.DontFragment {
		t.Error("a packet emitted as a probe reached the socket without the don't-fragment bit")
	}
	if ordinary.DontFragment {
		t.Error("an ordinary packet reached the socket marked don't-fragment")
	}
	if probe.Type != outgoing || ordinary.Type != outgoing {
		t.Error("emitPacket produced something other than an outgoing event")
	}
}

// ErrDontFragmentUnsupported must be matchable with errors.Is through a wrap,
// because writeDatagram tests it that way.
func TestDontFragmentUnsupportedIsMatchable(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), ErrDontFragmentUnsupported)
	if !errors.Is(wrapped, ErrDontFragmentUnsupported) {
		t.Error("ErrDontFragmentUnsupported does not survive wrapping")
	}
}
