//go:build linux || darwin || freebsd || windows

package utp_go

import (
	"errors"
	"net"
	"testing"
)

// The socket option has an effect, on a real socket, observed rather than
// assumed.
//
// Every other test in this area checks that the *request* travels: that a
// probe is marked, that the mark reaches the Conn, that a Conn which cannot
// honour it still sends. None of them can tell whether setDontFragment
// actually does anything, and a setsockopt that silently succeeded on the
// wrong option would pass all of them.
//
// This one puts a datagram larger than the outbound interface's MTU on a real
// socket. Without the bit the kernel fragments it and the send succeeds. With
// the bit there is nothing the kernel may do but refuse, and the send fails
// with EMSGSIZE. The contrast is the assertion: both halves must hold, so
// the test fails if the option stops working *or* if the environment stops
// being one where the difference is visible.
func TestDontFragmentActuallySetsTheBit(t *testing.T) {
	// A destination off-link, so the route leaves by a real interface rather
	// than loopback. Nothing has to be listening: the send either is refused
	// by the local kernel or is handed to the interface, and both answers
	// arrive before anything leaves the machine.
	dst := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 9}

	mtu, ok := pathMTUFor(dst)
	if !ok {
		t.Skip("no route to an off-link address, so there is no interface MTU to exceed")
	}
	if mtu <= 0 || mtu > 65000 {
		t.Skipf("outbound interface reports a %d-byte datagram limit; nothing sendable exceeds it", mtu)
	}

	base, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Skipf("no UDP socket available: %v", err)
	}
	defer base.Close()

	oversized := make([]byte, mtu+64)
	fits := make([]byte, 128)

	// Without the bit: the kernel fragments, and the send succeeds.
	if err := setDontFragment(base, false); err != nil {
		if errors.Is(err, ErrDontFragmentUnsupported) {
			t.Skip("this platform has no per-packet don't-fragment option")
		}
		t.Fatalf("clearing the bit: %v", err)
	}
	if _, err := base.WriteToUDP(oversized, dst); err != nil {
		t.Skipf("a %d-byte datagram will not send even fragmentable (%v), so this "+
			"environment cannot show the contrast", len(oversized), err)
	}

	// With the bit: the same datagram must be refused.
	if err := setDontFragment(base, true); err != nil {
		if errors.Is(err, ErrDontFragmentUnsupported) {
			t.Skip("this platform has no per-packet don't-fragment option")
		}
		t.Fatalf("setting the bit: %v", err)
	}
	if _, err := base.WriteToUDP(oversized, dst); err == nil {
		t.Fatalf("a %d-byte datagram was accepted with don't-fragment set, on a path "+
			"whose limit is %d. The option is not taking effect.", len(oversized), mtu)
	}

	// And the bit must not break ordinary sends that do fit.
	if _, err := base.WriteToUDP(fits, dst); err != nil {
		t.Errorf("a %d-byte datagram was refused with don't-fragment set: %v", len(fits), err)
	}

	// Clearing it must restore fragmentation, or every later packet on this
	// socket would be unfragmentable -- the thing libutp is explicit about
	// not wanting (utp_internal.cpp:898-905).
	if err := setDontFragment(base, false); err != nil {
		t.Fatalf("clearing the bit again: %v", err)
	}
	if _, err := base.WriteToUDP(oversized, dst); err != nil {
		t.Errorf("the oversized datagram is still refused after clearing the bit: %v", err)
	}
}

// The same contrast through the public path: UdpConn.WriteToDontFragment
// against UdpConn.WriteTo.
func TestUdpConnWriteToDontFragment(t *testing.T) {
	dst := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 9}
	mtu, ok := pathMTUFor(dst)
	if !ok || mtu <= 0 || mtu > 65000 {
		t.Skip("no off-link route with a usable MTU")
	}

	base, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Skipf("no UDP socket available: %v", err)
	}
	defer base.Close()
	c := &UdpConn{base: base}
	peer := NewUdpPeer(dst)

	oversized := make([]byte, mtu+64)
	if _, err := c.WriteTo(oversized, peer); err != nil {
		t.Skipf("a %d-byte datagram will not send even fragmentable: %v", len(oversized), err)
	}

	switch _, err := c.WriteToDontFragment(oversized, peer); {
	case err == nil:
		t.Fatal("an oversized datagram was accepted with don't-fragment set")
	case errors.Is(err, ErrDontFragmentUnsupported):
		t.Skip("this platform has no per-packet don't-fragment option")
	}

	// The bit must be cleared again afterwards, including after that failure.
	// If it leaked, this send would fail too.
	if _, err := c.WriteTo(oversized, peer); err != nil {
		t.Errorf("the don't-fragment bit leaked past WriteToDontFragment: a later "+
			"ordinary write of %d bytes failed with %v", len(oversized), err)
	}
}
