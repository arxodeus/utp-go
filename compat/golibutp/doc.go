// Package utp is a drop-in replacement for github.com/anacrolix/go-libutp,
// implemented in pure Go without cgo.
//
// A program, or anything it depends on, that imports go-libutp can use this
// package instead with one line in its go.mod:
//
//	replace github.com/anacrolix/go-libutp => github.com/arxodeus/utp/compat/golibutp <version>
//
// Every exported name of go-libutp v1.5.1 is provided with the same
// signature: NewSocket, NewSocketFromPacketConn, WithLogger, Socket, Conn,
// Option and its constants, FirewallCallback, Logger, WriteStatus,
// ErrConnClosed and TimedOut.
//
// Differences from go-libutp:
//
//   - Socket.SetDeadline, SetReadDeadline and SetWriteDeadline are
//     implemented.
//   - The firewall callback is not called with the package's lock held.
//   - Incoming connections waiting for Accept are held up to the connection
//     limit rather than closed once five are queued.
//   - An out-of-range option value is refused with -1 rather than aborting.
//   - RecvBuffer sizes both the receive and the send buffer
//     (utp.ConnectionConfig.BufferSize). SendBuffer is the ceiling on the
//     congestion window.
//   - expvar counters are not published.
package utp
