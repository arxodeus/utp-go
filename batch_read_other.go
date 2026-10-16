//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package utp

// udpConnReader keeps nothing: there are no batched reads here.
type udpConnReader struct{}

// readBatch is unsupported here; UtpSocket.readLoop falls back to one
// datagram per read, each its own batch.
func (udpConnReader) readBatch() ([]datagram, error) {
	return nil, errBatchReadUnsupported
}
