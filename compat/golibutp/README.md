# golibutp

A drop-in replacement for
[github.com/anacrolix/go-libutp](https://github.com/anacrolix/go-libutp): the
same package API, implemented on [github.com/arxodeus/utp](../../) in pure Go,
without cgo.

## Usage

Add one line to the go.mod of the program being built:

```
replace github.com/anacrolix/go-libutp => github.com/arxodeus/utp/compat/golibutp <version>
```

No imports or code change. Every package that imports go-libutp, including
dependencies such as [anacrolix/torrent](https://github.com/anacrolix/torrent),
uses this package instead. A `replace` only takes effect in the main module, so
it belongs in the application's go.mod.

## Differences from go-libutp

- `Socket.SetDeadline`, `SetReadDeadline` and `SetWriteDeadline` are
  implemented.
- Firewall callbacks are not called with the package lock held, and the
  asynchronous callback is consulted only for connection requests.
- Incoming connections are answered as they arrive and held until `Accept`
  takes them, up to the socket's connection limit, rather than a backlog of
  five.
- Invalid option values are refused with -1.
- An accepted connection can write before the dialer has sent anything.
- `RecvBuffer` sizes both the send and receive buffers. `SendBuffer` is the
  upper bound on the congestion window; `Conn.WriteBufferLen` and
  `SetWriteBufferLen` read and set it on a live connection.
- expvar counters are not published.

## Tests

go-libutp v1.5.1's own tests are included unchanged, except
`accept_write_test.go`, which is adapted because an accepted connection here
can write immediately. `compat_test.go` covers the differences above.

## Versioning

This module is tagged `compat/golibutp/vX.Y.Z` and requires the matching
release of `github.com/arxodeus/utp`.

## License

The test files taken from go-libutp are under its license,
[LICENSE.go-libutp](LICENSE.go-libutp) (MIT). Everything else is under this
repository's [LICENSE](../../LICENSE).
