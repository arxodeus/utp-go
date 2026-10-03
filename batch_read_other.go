//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package utp_go

// readBatch is unsupported here; UtpSocket.readLoop falls back to one
// datagram per read, each its own batch.
func (c *UdpConn) readBatch() ([]datagram, error) {
	return nil, errBatchReadUnsupported
}

// recvmmsgSlots is what Linux's drain uses; here there is none.
type recvmmsgSlots struct{}
