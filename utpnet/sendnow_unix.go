//go:build unix

package utpnet

import (
	"errors"
	"net"
	"sync"
	"syscall"
)

// sendNow makes one non-blocking attempt to send p to to. It reports sent
// false when the send would block, or cannot be made this way; the caller
// then hands the datagram to the writer, whose wait the write deadline can
// end.
//
// The send runs under RawConn.Control, not RawConn.Write: Write first takes
// the descriptor's write lock, which another write on the port may hold
// indefinitely. A UDP datagram is sent whole, so concurrent sends need no
// lock.
func (r *rawSender) sendNow(p []byte, to *net.UDPAddr) (n int, sent bool, err error) {
	if r == nil {
		return 0, false, nil
	}
	a := sendAttempts.Get().(*sendAttempt)
	defer sendAttempts.Put(a)
	if !a.address(r.family, to) {
		return 0, false, nil
	}
	a.p, a.err = p, nil
	cerr := r.rc.Control(a.do)
	serr := a.err
	a.p, a.sa = nil, nil
	if cerr != nil {
		return 0, true, cerr
	}
	// An interrupted send also goes to the writer, which retries it.
	if errors.Is(serr, syscall.EAGAIN) || errors.Is(serr, syscall.EWOULDBLOCK) || errors.Is(serr, syscall.EINTR) {
		return 0, false, nil
	}
	if serr != nil {
		return 0, true, &net.OpError{Op: "write", Net: "udp", Source: r.c.LocalAddr(), Addr: to, Err: serr}
	}
	return len(p), true, nil
}

// sendAttempt is one sendNow call's state. It is pooled together with the
// function bound to it, so an attempt does not allocate.
type sendAttempt struct {
	p   []byte
	err error
	sa  syscall.Sockaddr
	sa4 syscall.SockaddrInet4
	sa6 syscall.SockaddrInet6
	do  func(fd uintptr)
}

var sendAttempts = sync.Pool{New: func() any {
	a := &sendAttempt{}
	a.do = func(fd uintptr) {
		a.err = syscall.Sendto(int(fd), a.p, 0, a.sa)
	}
	return a
}}

// address sets a.sa to to in the socket's address family. A zoned address
// is left to the writer, which can resolve the zone.
func (a *sendAttempt) address(family int, to *net.UDPAddr) bool {
	if to == nil || to.Zone != "" || to.Port < 0 || to.Port > 0xffff {
		return false
	}
	switch family {
	case syscall.AF_INET:
		ip4 := to.IP.To4()
		if ip4 == nil {
			return false
		}
		a.sa4.Port = to.Port
		copy(a.sa4.Addr[:], ip4)
		a.sa = &a.sa4
		return true
	case syscall.AF_INET6:
		ip := to.IP.To16()
		if ip == nil {
			return false
		}
		a.sa6.Port, a.sa6.ZoneId = to.Port, 0
		copy(a.sa6.Addr[:], ip)
		a.sa = &a.sa6
		return true
	}
	return false
}

// rawSender sends directly on a UDP socket through its RawConn.
type rawSender struct {
	c      *net.UDPConn
	rc     syscall.RawConn
	family int
}

// newRawSender returns a rawSender for c, or nil if c's address family
// cannot be determined.
func newRawSender(c *net.UDPConn) *rawSender {
	rc, err := c.SyscallConn()
	if err != nil {
		return nil
	}
	family := 0
	_ = rc.Control(func(fd uintptr) {
		switch sa, err := syscall.Getsockname(int(fd)); {
		case err != nil:
		case isInet4(sa):
			family = syscall.AF_INET
		case isInet6(sa):
			family = syscall.AF_INET6
		}
	})
	if family == 0 {
		return nil
	}
	return &rawSender{c: c, rc: rc, family: family}
}

func isInet4(sa syscall.Sockaddr) bool { _, ok := sa.(*syscall.SockaddrInet4); return ok }
func isInet6(sa syscall.Sockaddr) bool { _, ok := sa.(*syscall.SockaddrInet6); return ok }
