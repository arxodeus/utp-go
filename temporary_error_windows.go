//go:build windows

package utp

import (
	"errors"
	"syscall"
)

const wsaEMsgSize = syscall.Errno(10040)

// isPacketTooBig reports whether err is WSAEMSGSIZE, which Windows returns
// for a datagram larger than the read buffer.
func isPacketTooBig(err error) bool {
	return errors.Is(err, wsaEMsgSize)
}
