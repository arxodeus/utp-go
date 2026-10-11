# go-libutp, without libutp

A drop-in replacement for
[github.com/anacrolix/go-libutp](https://github.com/anacrolix/go-libutp): the
same package API, on this library instead of libutp. No cgo, no C++.

## Using it

One line in the go.mod of the program being built:

```
replace github.com/anacrolix/go-libutp => github.com/zen-eth/utp-go/compat/golibutp <version>
```

Nothing else changes: not the imports, not the code, not the build flags.
Everything that imports go-libutp gets this package, including dependencies.
That covers [anacrolix/torrent](https://github.com/anacrolix/torrent), which
uses go-libutp in its default (cgo) build. A `replace` only takes effect in the
main module, so it is the application's go.mod that carries it, never a
library's.

For a checkout, point it at the directory instead:

```
replace github.com/anacrolix/go-libutp => /path/to/utp-go/compat/golibutp
```

## What is checked

- **go-libutp's own tests, unchanged.** `utp_test.go`, `socket_test.go`,
  `conn_test.go`, `callbacks_test.go`, `logging_test.go` and `bench_test.go`
  are go-libutp v1.5.1's files, copied as they are. They include
  `golang.org/x/net/nettest`'s `net.Conn` suite over IPv4 and IPv6, dial
  timeouts, dialling one's own socket, a dial whose sends fail, and use after
  close. All pass, under `-race` too.
- **One go-libutp test, adapted.** `accept_write_test.go` asserts a libutp
  limitation this package does not have. libutp's acceptor cannot write until
  the dialler's first data packet arrives; here it can. The file says what was
  changed and why.
- **This package's own additions** (`compat_test.go`):
  - the firewall callbacks;
  - options reaching new connections, including one accepted by an `Accept`
    that was already waiting when the options changed;
  - `Conn.SetWriteBufferLen` on a live connection;
  - a dial whose sends fail ending at once;
  - non-uTP datagrams sharing the port;
  - `ErrConnClosed` after `Close`;
  - connections leaving the socket's books once they end;
  - `WriteStatus`;
  - the log options;
  - a dial completing before anyone calls `Accept`.

  Each test fails with the mechanism it covers removed.
- **A real BitTorrent client through torrent's own socket layer.**
  `integration/anacrolix.TestTorrentOverTheGoLibutpDropIn` builds torrent's
  default configuration with this `replace` and nothing else. It then checks:
  - torrent's uTP listener is this package's socket;
  - torrent installed its firewall callback on it;
  - a 4 MiB torrent transfers between two clients, verified against the
    torrent's piece hashes;
  - one client's DHT answers the other's ping over the same socket.

  With the `replace` removed, so that real go-libutp is used, the test fails.

## What differs from go-libutp

Each difference is something go-libutp or libutp could not do:

- **Socket deadlines.** `Socket.SetDeadline`, `SetReadDeadline` and
  `SetWriteDeadline` work. go-libutp panics with "not implemented".
- **Firewall callbacks run unlocked.** They are not called with the package
  lock held, so a callback that takes a lock the caller also holds cannot
  deadlock.
- **Firewall callbacks only see connection requests.** The asynchronous
  callback is asked only about connection requests, which is all go-libutp
  uses its answer for. go-libutp asks it about every datagram.
- **A deeper accept backlog.** Incoming connections queue 128 deep for
  `Accept`, where go-libutp closes any beyond 5.
- **Bad options fail cleanly.** A buffer option below one byte is refused
  with -1. libutp would abort the process on its assertion.
- **The acceptor can write first.** An accepted connection can write before
  the dialler has sent anything (see above).
- **RecvBuffer sets both buffers.** It sizes this library's send buffer as
  well as its receive buffer, because the two are one setting here
  (`utp.ConnectionConfig.BufferSize`). SendBuffer is libutp's `opt_sndbuf`,
  the ceiling on the congestion window, as it is in libutp. `Conn.WriteBufferLen`
  and `SetWriteBufferLen` read and set that ceiling on a live connection, as
  `utp_getsockopt` and `utp_setsockopt` do.
- **No expvars.** go-libutp's expvar counters are not published.

Underneath, the protocol is this library's. That is libutp's behaviour, and
where it differs the difference is recorded and measured: see the repository's
[COMPATIBILITY.md](../../COMPATIBILITY.md) and
[DEVIATIONS.md](../../DEVIATIONS.md).

## Publishing

This module requires `github.com/zen-eth/utp-go`, and for development it uses
the checkout it sits in (the `replace` in its go.mod). A `replace` in a
dependency is ignored, so for a downloaded copy to build, the version it
requires has to exist. That means a tagged release of the module path this
repository declares.

## Licence

The test files taken from go-libutp are under its licence,
[LICENSE.go-libutp](LICENSE.go-libutp) (MIT). Everything else is under this
repository's.
