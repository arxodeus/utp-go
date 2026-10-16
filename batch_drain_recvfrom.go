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

// drain appends what the socket holds to out, waiting for the first datagram
// if there is none: recvfrom until it would block, as libutp's embedder does.
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
					// Nothing yet: wait, unless something has come.
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

// peerFor returns the peer a datagram came from, as recvfrom reports its
// address. See peerForKey.
func (c *udpConn) peerFor(sa syscall.Sockaddr) *UdpPeer {
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
