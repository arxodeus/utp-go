//go:build netbsd || openbsd

package utp_go

import (
	"net"
	"syscall"
)

// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket.
//
// NetBSD and OpenBSD are IPv6-only here. Both define IPV6_DONTFRAG, and
// neither defines an IPv4 IP_DONTFRAG: on those systems the bit is not
// settable per socket for IPv4 at all. Guessing a number for it would be
// worse than not offering it -- socket option numbers are not shared across
// these kernels, as Darwin's 0x1c against FreeBSD's 0x43 shows, and a wrong
// guess would quietly set some unrelated option.
//
// So an IPv4 socket here returns ErrDontFragmentUnsupported and the probe
// goes out fragmentable, exactly as it did before any of this existed. An
// IPv6 socket gets the real thing, which is the case that needs it least,
// since IPv6 routers do not fragment in the first place.
func setDontFragment(c *net.UDPConn, on bool) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	v := 0
	if on {
		v = 1
	}
	var errV6 error
	if cerr := rc.Control(func(fd uintptr) {
		errV6 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_DONTFRAG, v)
	}); cerr != nil {
		return cerr
	}
	if errV6 != nil {
		return ErrDontFragmentUnsupported
	}
	return nil
}
