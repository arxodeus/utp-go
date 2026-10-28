//go:build netbsd || openbsd

package utp

import (
	"net"
	"syscall"
)

// setDontFragment sets or clears the don't-fragment bit for the next
// datagram sent on the socket.
//
// NetBSD and OpenBSD define IPV6_DONTFRAG but no IPv4 equivalent, so an IPv4
// socket returns ErrDontFragmentUnsupported and its MTU probes are sent
// fragmentable.
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
