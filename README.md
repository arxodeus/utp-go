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
| [`utp`](https://pkg.go.dev/github.com/arxodeus/utp) | The protocol: `UtpSocket` and `UtpStream`, with context-aware reads and writes. |
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

- Classic LEDBAT congestion control (RFC 6817) by default, with LEDBAT++ and
  RACK loss detection (RFC 8985) available per connection.
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

The library is wire-compatible with existing uTP implementations. The test
suite compares its behavior packet by packet against the reference
implementation and runs transfers against it, over real sockets and over
emulated links with loss, reordering and duplication.

## Testing

```sh
go test ./...          # full suite; the libutp comparisons need cgo
go test -short ./...   # quick run
go test -race ./...
```

Notes for contributors are in [docs/dev](docs/dev).

## Origin

This project began as a fork of
[zen-eth/utp-go](https://github.com/zen-eth/utp-go).

## License

MIT. See [LICENSE](LICENSE).
