//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package utp

import (
	"errors"
	"syscall"
)

// drainState is drain's receive buffer.
type drainState struct {
	scratch []byte
}

// drain appends the datagrams queued on the socket to out, waiting for the
// first one if none is queued. It reads until the socket would block.
func (c *udpConn) drain(rc syscall.RawConn, out *[]datagram) error {
	if c.scratch == nil {
		c.scratch = make([]byte, 65536)
	}
	scratch := c.scratch
	var readErr error
	err := rc.Read(func(fd uintptr) bool {
		for len(*out) < maxReadBatch {
			n, from, err := syscall.Recvfrom(int(fd), scratch, 0)
			if err != nil {
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
					// Nothing queued: wait, unless this call has already read something.
					return len(*out) > 0
				}
				readErr = err
				return true
			}
			peer := c.peerFor(from)
			if peer == nil {
				continue
			}
			payload := c.lend(scratch[:n])
			*out = append(*out, datagram{payload: payload, peer: peer, borrowed: true})
		}
		return true
	})
	if err != nil {
		return err
	}
	return readErr
}

// peerFor returns the peer for a source address reported by recvfrom.
func (c *udpConn) peerFor(sa syscall.Sockaddr) *UDPPeer {
	var k peerKey
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		copy(k.ip[:], a.Addr[:])
		k.port = a.Port
	case *syscall.SockaddrInet6:
		k.ip, k.port, k.zone, k.v6 = a.Addr, a.Port, a.ZoneId, true
	default:
		return nil
	}
	return c.peerForKey(k)
}
