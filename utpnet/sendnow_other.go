//go:build !unix

package utpnet

import "net"

// rawSender is a placeholder on platforms without a non-blocking direct send;
// every datagram goes through the writer goroutine.
type rawSender struct{}

func newRawSender(*net.UDPConn) *rawSender { return nil }

func (*rawSender) sendNow([]byte, *net.UDPAddr) (int, bool, error) { return 0, false, nil }
