//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp_go

import (
	"errors"
	"net"
	"sync"
	"syscall"
)

// maxReadBatch bounds one batch. libutp's embedders read until the socket
// would block, with no bound (the bridge in native/libutp does exactly that);
// this one only keeps a pathological burst from holding acknowledgements back
// indefinitely. A 4 MiB socket buffer holds about 3000 full-size datagrams.
const maxReadBatch = 4096

// readBatch returns every datagram the kernel already has queued, waiting only
// for the first one: libutp's embedder loop of recvfrom until EWOULDBLOCK,
// after which it calls utp_issue_deferred_acks once (utp.h:512-517;
// native/libutp/bridge.cpp). See UtpSocket.readLoop.
func (c *UdpConn) readBatch() ([]datagram, error) {
	rc, err := c.base.SyscallConn()
	if err != nil {
		return nil, err
	}
	if c.readScratch == nil {
		c.readScratch = make([]byte, 65536)
	}
	var out []datagram
	err = c.drain(rc, &out, true)
	return out, err
}

// drain appends what the socket holds to out. Blocking, it waits for the
// first datagram if there is none; otherwise it returns at once.
func (c *UdpConn) drain(rc syscall.RawConn, out *[]datagram, block bool) error {
	scratch := c.readScratch
	var readErr error
	err := rc.Read(func(fd uintptr) bool {
		for len(*out) < maxReadBatch {
			n, from, err := syscall.Recvfrom(int(fd), scratch, 0)
			if err != nil {
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
					// Nothing yet: wait if asked to and nothing has come.
					return !block || len(*out) > 0
				}
				readErr = err
				return true
			}
			addr := sockaddrToUDPAddr(from)
			if addr == nil {
				continue
			}
			payload := make([]byte, n)
			copy(payload, scratch[:n])
			*out = append(*out, datagram{payload: payload, peer: &UdpPeer{addr: addr}})
		}
		return true
	})
	if err != nil {
		return err
	}
	return readErr
}

// sockaddrToUDPAddr converts what recvfrom reports into the address
// net.UDPConn.ReadFrom would have returned, so a peer read in a batch hashes
// exactly as one read singly does.
func sockaddrToUDPAddr(sa syscall.Sockaddr) *net.UDPAddr {
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		ip := make(net.IP, net.IPv4len)
		copy(ip, a.Addr[:])
		return &net.UDPAddr{IP: ip, Port: a.Port}
	case *syscall.SockaddrInet6:
		ip := make(net.IP, net.IPv6len)
		copy(ip, a.Addr[:])
		return &net.UDPAddr{IP: ip, Port: a.Port, Zone: zoneName(a.ZoneId)}
	}
	return nil
}

var zoneNames sync.Map // uint32 -> string

// zoneName is the interface name for an IPv6 scope id, as net formats it: the
// name when the interface exists, the number otherwise.
func zoneName(id uint32) string {
	if id == 0 {
		return ""
	}
	if v, ok := zoneNames.Load(id); ok {
		return v.(string)
	}
	name := uitoa(id)
	if ifi, err := net.InterfaceByIndex(int(id)); err == nil {
		name = ifi.Name
	}
	zoneNames.Store(id, name)
	return name
}

func uitoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
