//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package utp

// udpConnReader is empty on platforms without batched reads.
type udpConnReader struct{}

// readBatch is not supported on this platform; the read loop falls back to
// one datagram per read.
func (udpConnReader) readBatch() ([]datagram, error) {
	return nil, errBatchReadUnsupported
}
