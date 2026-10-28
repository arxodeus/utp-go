package utp

import (
	"net"
	"syscall"
)

// Don't-fragment socket options from ws2ipdef.h, which package syscall does
// not define. Both have the value 14, at the IPPROTO_IP and IPPROTO_IPV6
// levels respectively.
const (
	windowsIPDontFragment = 14
	windowsIPv6DontFrag   = 14
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
