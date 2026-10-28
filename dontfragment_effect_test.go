//go:build linux || darwin || freebsd || windows

package utp

import (
	"errors"
	"net"
	"testing"
)

// The socket option has a real effect on a real socket. A datagram larger
// than the outbound interface's MTU is sent: without the bit the kernel
// fragments it and the send succeeds; with the bit the send fails with
// EMSGSIZE. Both halves must hold, so the test fails if the option stops
// working or if the environment stops showing the difference.
func TestDontFragmentActuallySetsTheBit(t *testing.T) {
	// An off-link destination, so the route uses a real interface rather
	// than loopback. Nothing need be listening: the local kernel either
	// refuses the send or hands it to the interface.
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

	// Clearing it must restore fragmentation, or every later packet on the
	// socket would be unfragmentable.
	if err := setDontFragment(base, false); err != nil {
		t.Fatalf("clearing the bit again: %v", err)
	}
	if _, err := base.WriteToUDP(oversized, dst); err != nil {
		t.Errorf("the oversized datagram is still refused after clearing the bit: %v", err)
	}
}

// The same contrast through the public path: udpConn.WriteToDontFragment
// against udpConn.WriteTo.
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
	c := &udpConn{base: base}
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
