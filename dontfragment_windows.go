package utp_go

import (
	"net"
	"syscall"
)

// Windows' don't-fragment options, which the standard library's syscall
// package does not define. Both are from ws2ipdef.h: IP_DONTFRAGMENT at the
// IPPROTO_IP level and IPV6_DONTFRAG at IPPROTO_IPV6. They share the value 14
// at their respective levels, which looks like a typo and is not.
const (
	windowsIPDontFragment = 14
	windowsIPv6DontFrag   = 14
)

// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket. See the Linux file for why this is a socket
// option toggled around a single send, and why succeeding if either address
// family works is the right test.
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
		h := syscall.Handle(fd)
		errV4 = syscall.SetsockoptInt(h, syscall.IPPROTO_IP, windowsIPDontFragment, v)
		errV6 = syscall.SetsockoptInt(h, syscall.IPPROTO_IPV6, windowsIPv6DontFrag, v)
	}); cerr != nil {
		return cerr
	}
	if errV4 == nil || errV6 == nil {
		return nil
	}
	return errV4
}
