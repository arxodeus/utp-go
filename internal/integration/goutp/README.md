# go-utp interop

A separate Go module that tests this library against the pure-Go engine of
go-utp, [`purego`](https://pkg.go.dev/github.com/anacrolix/go-utp/purego). It
is kept apart so that go-utp's dependencies stay out of the library's module
graph. No cgo is needed.

```
cd internal/integration/goutp && go test ./...
```

Each case runs with either side dialling.

- Over loopback UDP: one-way transfers in each direction, both directions at
  once with half-close, 16 concurrent connections, many small writes, and a
  request and response across a half-close.
- Over the emulated network in [`internal/netem`](../../netem): transfers in
  every combination of who dials and who sends, over links with delay and
  jitter, loss, reordering, duplication, a narrow bottleneck, and all of
  them together.

purego writes nothing on an accepted connection until the dialler has sent
data, as libutp does, so where purego accepts and is to send, the tests dial
and send one byte first, as a BitTorrent handshake would.
