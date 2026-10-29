# compat/golibutp/doc.go

Every comment in `compat/golibutp/doc.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
// Package utp is github.com/anacrolix/go-libutp's API on this library: a
// drop-in replacement for go-libutp, with no cgo and no libutp.
//
// A program -- or anything it depends on, anacrolix/torrent included -- that
// imports go-libutp uses this instead with one line in its go.mod:
//
//	replace github.com/anacrolix/go-libutp => github.com/arxodeus/utp/compat/golibutp <version>
//
// Every exported name go-libutp v1.5.1 has is here, with its signature:
// NewSocket, NewSocketFromPacketConn, WithLogger, Socket, Conn, Option and
// its constants, FirewallCallback, Logger, WriteStatus, ErrConnClosed and
// TimedOut. go-libutp's own tests run against this package unchanged (see
// golibutp_test.go and the files beside it), and the BitTorrent client in
// integration/anacrolix runs on it through torrent's own socket layer.
//
// What differs, each because go-libutp's behaviour was a limitation of
// libutp or of its binding:
//
//   - Socket.SetDeadline, SetReadDeadline and SetWriteDeadline work, where
//     go-libutp panics with "not implemented".
//   - A firewall callback is not called with this package's lock held, so
//     it cannot deadlock against it.
//   - Incoming connections waiting for Accept are held up to the connection
//     cap rather than closed once five are queued.
//   - Option values that libutp asserts on (a buffer below one byte) are
//     refused with -1 rather than aborting the process.
//   - RecvBuffer sizes this library's send buffer as well as its receive
//     buffer: the two are one setting here (utp.ConnectionConfig.BufferSize).
//     SendBuffer is libutp's opt_sndbuf, the ceiling on the congestion
//     window, as it is in libutp.
//   - go-libutp's expvar counters are not published.
```
