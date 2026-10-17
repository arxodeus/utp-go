//go:build windows

package utp

import (
	"errors"
	"syscall"
)

const wsaEMsgSize = syscall.Errno(10040)

// isPacketTooBig reports whether err is WSARecvFrom's WSAEMSGSIZE: a
// datagram that did not fit the buffer, of which Windows returns nothing.
func isPacketTooBig(err error) bool {
	return errors.Is(err, wsaEMsgSize)
}
