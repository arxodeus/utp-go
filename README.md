# utp-go

A Go implementation of the µTorrent Transport Protocol (uTP).

## About this fork

This is a fork of [`zen-eth/utp-go`](https://github.com/zen-eth/utp-go), aimed
at a uTP implementation a BitTorrent client can rely on and that builds with
`CGO_ENABLED=0`.

Upstream's own integration tests could not pass on a clean checkout. They now
do. See:

| Document | What it covers |
| --- | --- |
| [HARNESS.md](HARNESS.md) | The emulated network harness: what it models, what it measures, and the numbers proving it is sound. |
| [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) | **Read this first.** Root cause of every defect fixed, what has and has not been measured, and everything found but not fixed. |
| [REFERENCE.md](REFERENCE.md) | Which copy of libutp is normative, and proof the copy used for differential testing is protocol-identical to it. |
| [DEVIATIONS.md](DEVIATIONS.md) | Every intentional difference from libutp. |
| [UPSTREAM.md](UPSTREAM.md) | What should go back to `zen-eth/utp-go`, and in what order. |

Two things to be aware of before depending on this:

- **No interoperability testing against real libutp has been done.** Nothing
  here has been shown to talk to a real BitTorrent peer.
- Every measurement in these documents is **loopback on one machine**. There
  is no emulated-network harness, and the congestion controller's behaviour
  under loss, delay or competition has not been measured.

## Testing

```sh
go test ./...                                  # full suite
go test ./netem/                               # the network harness gate
go test -race ./...                            # race detector
scripts/check-libutp-reference.sh              # verify the pinned libutp reference
```

`TestManyConcurrentTransfers` runs 1000 concurrent 1 MB transfers by default
and peaks at roughly 5 GB RSS. Set `UTP_TEST_TRANSFERS` to run it smaller —
which is necessary under `-race`.

The cgo differential-testing harness in `native/cgo` needs a C libutp that is
not vendored here, and is behind a build tag:

```sh
go build -tags utp_cgo_harness ./native/cgo
```

## Licence

MIT. libutp, from which behaviour here is derived, is also MIT.
