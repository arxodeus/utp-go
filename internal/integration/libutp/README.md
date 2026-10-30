# libutp interoperability

A separate Go module that tests this library against BitTorrent's libutp,
vendored here unmodified (see [VENDOR.md](VENDOR.md)). It needs cgo, and is
kept apart so that the library's module contains no C code.

```
cd internal/integration/libutp && go test ./...
```

- The package at the module root is libutp with a bridge: a `Peer` that runs
  it over a real UDP socket, and a `Driver` with a virtual clock and packets
  in and out by explicit call. Its tests transfer data between libutp and
  this library over loopback, concurrently, in small writes, and through a
  relay that damages the path.
- [`netem`](netem) runs the same over the emulated network in
  [`internal/netem`](../../netem): transfers in every direction over lossy,
  reordering and narrow links, half-close, idle decay, path MTU, and how
  each implementation shares a link with a loss-based flow.
- [`cmd/libutp-driver`](cmd/libutp-driver) serves one `Driver` over standard
  input and output.

## Recorded transcripts

The root package's conformance tests compare this library with libutp
packet by packet. They run without cgo by replaying what libutp did,
recorded in [`testdata/libutp`](../../../testdata/libutp). To run them
against libutp itself, and to re-record the transcripts after a change in
behaviour:

```sh
cd internal/integration/libutp
go build -o /tmp/libutp-driver ./cmd/libutp-driver
cd ../../..
UTP_LIBUTP_DRIVER=/tmp/libutp-driver go test .                       # live
UTP_LIBUTP_DRIVER=/tmp/libutp-driver UTP_LIBUTP_RECORD=1 go test .   # re-record
```

Record with the whole package, not a subset: each transcript file is
rewritten from the tests that ran. CI runs the tests live on every push and
fails if the recorded transcripts are out of date.
