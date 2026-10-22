//go:build !unix

package utpnet

import "net"

// rawSender has no single non-blocking attempt here, so every datagram goes
// to the writer. See sendnow_unix.go.
type rawSender struct{}

func newRawSender(*net.UDPConn) *rawSender { return nil }

func (*rawSender) sendNow([]byte, *net.UDPAddr) (int, bool, error) { return 0, false, nil }
