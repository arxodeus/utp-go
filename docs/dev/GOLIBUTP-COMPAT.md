<!-- compat/golibutp/README.md before the release cleanup: what is checked, throughput measurements and details of each difference. -->

# go-libutp, without libutp

A drop-in replacement for
[github.com/anacrolix/go-libutp](https://github.com/anacrolix/go-libutp): the
same package API, on this library instead of libutp. No cgo, no C++.

## Using it

One line in the go.mod of the program being built:

```
replace github.com/anacrolix/go-libutp => github.com/arxodeus/utp/compat/golibutp <version>
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

## Throughput

go-libutp's own benchmark, `BenchmarkThroughput10MB` and `100MB`, measures one
connection over loopback on one socket pair. It was run on real go-libutp
v1.5.1 (cgo, libutp) and on this package, interleaved, ten rounds of three
transfers each, on one machine:

| Benchmark | go-libutp | this package | Mann-Whitney z |
| --- | --- | --- | --- |
| 10 MB | 15.2 MB/s median (6.8-17.0) | 184.2 MB/s (163.7-201.2) | 3.78 |
| 100 MB | 19.7 MB/s (13.9-21.8) | 207.3 MB/s (87.6-214.6) | 3.78 |
| Bytes allocated per 100 MB | 39.6 MB | 168 MB (272 MB before the read pool) | |

This is loopback, where nothing is queued and nothing is lost, so it measures
how fast each implementation turns packets around, not how it behaves on a
network. The repository's emulated-network comparisons
([BENCHMARKS.md](BENCHMARKS.md)) are the ones that say that.

The allocation figures are not like for like. go-libutp's counts only the
Go heap; libutp allocates each packet it builds or holds out of order with
`malloc`, which Go's counter never sees. Of this package's 2.7 bytes per byte
moved, a memory profile of the 100 MB benchmark put 39% in the copy
`utpnet` makes of every datagram it reads, so that it can route it to uTP or
to `ReadFrom`, and 38% in the copy a `Write` makes so that it can return
before its bytes are sent (the send buffer's, KNOWN-LIMITATIONS.md, "The send
buffer retained the caller's slice"). The first now goes into pooled
buffers, released as soon as the datagram is copied out: 168 MB allocated
per 100 MB against 272 before, 1.7 bytes per byte moved, and faster for it --
153.9 against 144.9 MB/s at 100 MB, 150.4 against 129.4 at 10 MB (ten
interleaved rounds each, z = 2.27 and 2.12; a different day from the table's
figures, which is why the comparison is interleaved). The second is the
cost of a write that returns before its bytes are sent, and stays.

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
- **No five-connection backlog.** Incoming connections are answered as they
  arrive, as libutp answers them, and held until `Accept` takes them, up to
  the socket's connection cap (libutp's 3000). go-libutp closes any beyond 5
  waiting.
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
[COMPATIBILITY.md](COMPATIBILITY.md) and
[DEVIATIONS.md](DEVIATIONS.md).

## Publishing

This module requires `github.com/arxodeus/utp` v0.1.0, and for development it
uses the checkout it sits in (the `replace` in its go.mod). A `replace` in a
dependency is ignored, so a downloaded copy builds against the v0.1.0 it
requires: that tag has to exist before this module's own,
`compat/golibutp/v0.1.0` (it lives in a subdirectory, so its tags carry the
directory as a prefix). The order, and the check that a clean consumer can
build both, are in [RELEASING.md](RELEASING.md).

## Licence

The test files taken from go-libutp are under its licence,
[LICENSE.go-libutp](../../compat/golibutp/LICENSE.go-libutp) (MIT). Everything else is under this
repository's.
