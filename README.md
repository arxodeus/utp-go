# utp

A pure-Go implementation of the Micro Transport Protocol
([uTP, BEP 29](https://www.bittorrent.org/beps/bep_0029.html)): a reliable,
ordered byte stream over UDP with delay-based congestion control, as used by
BitTorrent. It needs no cgo and builds with `CGO_ENABLED=0`.

```
go get github.com/arxodeus/utp
```

## Packages

| Package | Purpose |
| --- | --- |
| [`utp`](https://pkg.go.dev/github.com/arxodeus/utp) | The protocol: `utp.Socket` and `utp.Stream`, with context-aware reads and writes. |
| [`utpnet`](https://pkg.go.dev/github.com/arxodeus/utp/utpnet) | The same, as `net.Conn` and `net.PacketConn`, with non-uTP datagrams on the port passed through to `ReadFrom`. |
| [`compat/golibutp`](compat/golibutp) | A drop-in replacement for `github.com/anacrolix/go-libutp`, as a separate module. |

## Usage

```go
sock, err := utpnet.Listen(ctx, "udp", ":6881", nil)
if err != nil {
	return err
}
defer sock.Close()

// Accept incoming connections.
go func() {
	for {
		conn, err := sock.Accept()
		if err != nil {
			return
		}
		go handle(conn)
	}
}()

// Dial out from the same port.
conn, err := sock.DialContext(ctx, "udp", "203.0.113.7:6881")
```

`sock` is also a `net.PacketConn`: datagrams that are not uTP, such as DHT
traffic sharing the port, are returned by `ReadFrom`, and `WriteTo` sends on the
same port.

## Features

- Classic LEDBAT congestion control (RFC 6817) by default. LEDBAT++ and RACK
  loss detection (RFC 8985) are available per connection as experimental
  options.
- Selective acknowledgements, fast retransmission, a tail loss probe, and
  undoing of window reductions caused by reordering.
- Path MTU discovery, with don't-fragment probes where the platform supports
  it and optional ICMP input.
- Half-close in both directions (`CloseWrite`, `CloseRead`).
- Idle connections release their goroutine and buffers.
- Connection limits, a firewall callback for refusing peers before any state
  is created, and per-connection metrics.
- Batched socket reads (`recvmmsg` on Linux) and one acknowledgement per batch.
- Structured logging through `log/slog`.

## Compatibility

With the default configuration the library is wire-compatible with the
reference uTP implementation, libutp, and with implementations built on it.
The experimental options change only how this side paces and detects loss;
they need nothing from the peer.

## Status and limitations

This is a 0.x release: the API may still change before 1.0.

- All measurements so far are on a single machine, over loopback and an
  emulated network. Nothing has yet been measured over wide-area paths.
- LEDBAT++ is experimental. A LEDBAT++ flow that joins a link where another
  LEDBAT++ flow is already running takes about 70% of the link while both
  run, rather than an even share.
- RACK loss detection is experimental.
- Classic LEDBAT, like libutp's, does not yield fully to loss-based TCP
  traffic: it keeps about 69% of a shared bottleneck, by design of the
  protocol.
- When a connection's event queue is full, further packets for it are
  dropped and counted in `Socket.PacketsDroppedFullConnQueue`.
- The full test suite, the race detector and fuzzing run on Linux only;
  macOS and Windows run the short suite.
- The Go version in go.mod is the minimum, not a recommendation: older
  standard libraries have known vulnerabilities this code reaches. Build
  with a current Go release.

## Testing

```sh
go test ./...          # full suite; the reference-comparison tests need cgo
go test -short ./...   # quick run
go test -race ./...
```

## Origin

This project began as a fork of
[zen-eth/utp-go](https://github.com/zen-eth/utp-go). See [NOTICE](NOTICE).

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
