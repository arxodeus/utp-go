package utp_go

import (
	"net"
	"syscall"
)

// Darwin's don't-fragment options, which the standard library's syscall
// package does not define. The values are golang.org/x/sys/unix's, generated
// from the system headers: IP_DONTFRAG in netinet/in.h and IPV6_DONTFRAG in
// netinet6/in6.h.
//
// They are spelled out here rather than taken from x/sys so that this stays a
// module with no dependency added for one integer -- the same reasoning
// path_mtu.go records for not reaching for getsockopt(IP_MTU).
//
// Note that Darwin's IP_DONTFRAG is not FreeBSD's. Darwin uses 0x1c and
// FreeBSD 0x43, which is why these are separate files rather than one shared
// "BSD" one.
const (
	darwinIPDontFrag   = 0x1c
	darwinIPv6DontFrag = 0x3e
)

// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket. See the Linux file for why this is a socket
// option toggled around a single send rather than a per-datagram flag, and
// why succeeding if either address family works is the right test.
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
		errV4 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, darwinIPDontFrag, v)
		errV6 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, darwinIPv6DontFrag, v)
	}); cerr != nil {
		return cerr
	}
	if errV4 == nil || errV6 == nil {
		return nil
	}
	return errV4
}
