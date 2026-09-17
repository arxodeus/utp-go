package utp_go

import (
	"net"
	"syscall"
)

// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket. FreeBSD defines both options in the standard
// library's syscall package, so unlike Darwin nothing has to be spelled out
// here. See the Linux file for why this is a socket option toggled around a
// single send, and why succeeding if either address family works is the right
// test.
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
