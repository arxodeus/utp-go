package utp_go

import (
	"net"
	"syscall"
)

// setDontFragment turns the IPv4 don't-fragment bit, and its IPv6 equivalent,
// on or off for the next datagram sent on this socket.
//
// Linux has no per-datagram control message for this -- it is a socket option
// on every platform that offers it at all -- so the caller sets it, sends one
// datagram, and clears it. That is only safe because every datagram this
// library sends leaves through one goroutine, UtpSocket.writeLoop.
//
// IP_PMTUDISC_PROBE rather than IP_PMTUDISC_DO. Both set the bit; the
// difference is whose path-MTU estimate wins. DO defers to the kernel's
// cached estimate for the route and fails the send with EMSGSIZE when the
// datagram exceeds it. PROBE sets the bit and sends the datagram regardless.
//
// This library runs its own search, with its own floor, ceiling and history
// (mtu.go), and libutp does the same. A kernel estimate refusing the send
// would silently cap that search at whatever the kernel had already learned,
// which is the opposite of probing: the point of a probe is to find out
// whether a size works, not to be told what the kernel currently believes.
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

	// One of the two always fails: a socket has one address family, and the
	// option for the other is not recognised on it. Succeeding if either
	// worked is what makes this correct for a v4 socket, a v6 socket and a
	// dual-stack v6 socket carrying v4-mapped addresses alike, without having
	// to work out which it is.
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
