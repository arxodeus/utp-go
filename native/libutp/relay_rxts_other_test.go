//go:build cgo && !linux

package libutp_test

import (
	"net"
	"time"
)

func enableRxTimestamps(*net.UDPConn) {}

func rxTimestamp([]byte) (time.Time, bool) { return time.Time{}, false }
