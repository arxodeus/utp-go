//go:build unix

package utpnet

import (
	"errors"
	"net"
	"sync"
	"syscall"
)

// sendNow makes one attempt to send p to to on c without waiting: the
// common case, which the kernel takes at once. It reports sent false only
// when the send would have had to wait -- a full send buffer -- or cannot be
// made this way, and then the caller hands the datagram to the writer, whose
// wait the write deadline can end.
//
// A Go socket is non-blocking underneath, and a RawConn.Write whose function
// always reports done is a single attempt: it never waits for the socket to
// become writable, which is the wait no deadline set afterwards could end.
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
	cerr := r.rc.Write(a.do)
	serr := a.err
	a.p, a.sa = nil, nil
	if cerr != nil {
		return 0, true, cerr
	}
	if errors.Is(serr, syscall.EAGAIN) || errors.Is(serr, syscall.EWOULDBLOCK) {
		return 0, false, nil
	}
	if serr != nil {
		return 0, true, &net.OpError{Op: "write", Net: "udp", Source: r.c.LocalAddr(), Addr: to, Err: serr}
	}
	return len(p), true, nil
}

// sendAttempt is one sendNow's state, pooled with the function RawConn.Write
// calls bound to it once, so that an attempt allocates nothing: a closure or
// a sockaddr made per datagram cost four allocations a send.
type sendAttempt struct {
	p   []byte
	err error
	sa  syscall.Sockaddr
	sa4 syscall.SockaddrInet4
	sa6 syscall.SockaddrInet6
	do  func(fd uintptr) bool
}

var sendAttempts = sync.Pool{New: func() any {
	a := &sendAttempt{}
	a.do = func(fd uintptr) bool {
		a.err = syscall.Sendto(int(fd), a.p, 0, a.sa)
		return true
	}
	return a
}}

// address sets a.sa to to as the socket of the given family addresses it. A
// zoned address is left to the writer, which has net's interface lookup.
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

// rawSender is a UDP socket as sendNow uses it, its RawConn made once.
type rawSender struct {
	c      *net.UDPConn
	rc     syscall.RawConn
	family int
}

// newRawSender is c ready for sendNow, or nil if its address family cannot
// be told: an IPv6 socket may be dual-stack, and then an IPv4 destination
// goes to it IPv4-mapped.
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
