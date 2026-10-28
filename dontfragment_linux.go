package utp

import (
	"net"
	"syscall"
)

// setDontFragment sets or clears the don't-fragment bit, IPv4 and IPv6, for
// the next datagram sent on the socket. The option is per socket, so the
// caller sets it, sends one datagram and clears it, with no other write in
// between (UtpSocket.writeDatagram).
//
// It uses IP_PMTUDISC_PROBE rather than IP_PMTUDISC_DO: both set the bit,
// but DO refuses a datagram larger than the kernel's cached path MTU, which
// would cap the package's own MTU search at whatever the kernel has learned.
func setDontFragment(c *net.UDPConn, on bool) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	v4 := syscall.IP_PMTUDISC_DONT
	v6 := syscall.IPV6_PMTUDISC_DONT
	if on {
		v4 = syscall.IP_PMTUDISC_PROBE
		v6 = syscall.IPV6_PMTUDISC_PROBE
	}

	// A socket has one address family, so one of the two options always fails.
	// Succeeding if either works covers IPv4, IPv6 and dual-stack sockets.
	var errV4, errV6 error
	if cerr := rc.Control(func(fd uintptr) {
		errV4 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MTU_DISCOVER, v4)
		errV6 = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MTU_DISCOVER, v6)
	}); cerr != nil {
		return cerr
	}
	if errV4 == nil || errV6 == nil {
		return nil
	}
	return errV4
}
