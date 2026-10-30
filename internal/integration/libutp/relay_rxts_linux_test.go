//go:build cgo && linux

package libutp_test

import (
	"net"
	"syscall"
	"time"
	"unsafe"
)

// enableRxTimestamps asks the kernel to stamp each datagram c receives with
// the time it arrived (SO_TIMESTAMPNS).
func enableRxTimestamps(c *net.UDPConn) {
	if raw, err := c.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_TIMESTAMPNS, 1)
		})
	}
}

// rxTimestamp is the arrival time the kernel attached to a datagram, if any.
func rxTimestamp(oob []byte) (time.Time, bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return time.Time{}, false
	}
	for _, m := range msgs {
		if m.Header.Level == syscall.SOL_SOCKET && m.Header.Type == syscall.SO_TIMESTAMPNS &&
			len(m.Data) >= int(unsafe.Sizeof(syscall.Timespec{})) {
			ts := (*syscall.Timespec)(unsafe.Pointer(&m.Data[0]))
			return time.Unix(ts.Sec, ts.Nsec), true
		}
	}
	return time.Time{}, false
}
