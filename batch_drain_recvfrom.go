//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package utp_go

import (
	"errors"
	"syscall"
)

// recvmmsgSlots is what Linux's drain uses; here there is no recvmmsg.
type recvmmsgSlots struct{}

// drain appends what the socket holds to out, waiting for the first datagram
// if there is none: recvfrom until it would block, as libutp's embedder does.
func (c *UdpConn) drain(rc syscall.RawConn, out *[]datagram) error {
	if c.readScratch == nil {
		c.readScratch = make([]byte, 65536)
	}
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
