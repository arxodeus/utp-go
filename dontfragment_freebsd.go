package utp

import (
	"net"
	"syscall"
)

// setDontFragment sets or clears the don't-fragment bit for the next
// datagram sent on the socket. It succeeds if either address family accepts
// the option.
func setDontFragment(c *net.UDPConn, on bool) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	v := 0
	if on {
		v = 1
	}
	var errV4, errV6 error
	if cerr := rc.Control(func(fd uintptr) {
		errV4 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_DONTFRAG, v)
		errV6 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_DONTFRAG, v)
	}); cerr != nil {
		return cerr
	}
	if errV4 == nil || errV6 == nil {
		return nil
	}
	return errV4
}
