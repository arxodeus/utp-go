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
| [CONFORMANCE.md](CONFORMANCE.md) | The conformance corpus: how two implementations are compared packet by packet, what it found, and what it does not cover. |
| [HARNESS.md](HARNESS.md) | The emulated network harness: what it models, what it measures, and the numbers proving it is sound. |
| [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) | **Read this first.** Root cause of every defect fixed, what has and has not been measured, and everything found but not fixed. |
| [BENCHMARKS.md](BENCHMARKS.md) | Congestion-control measurements: LEDBAT against LEDBAT++, the latecomer experiment, and what the numbers do not prove. |
| [REFERENCE.md](REFERENCE.md) | Which copy of libutp is normative, and proof the copy used for differential testing is protocol-identical to it. |
| [DEVIATIONS.md](DEVIATIONS.md) | Every intentional difference from libutp. |
| [UPSTREAM.md](UPSTREAM.md) | What should go back to `zen-eth/utp-go`, and in what order. |
| [native/libutp/VENDOR.md](native/libutp/VENDOR.md) | The vendored libutp: provenance, what was taken, and how the bridge works. |

Two things to be aware of before depending on this:

- **Interoperability with real libutp is tested** -- see `native/libutp/`,
  which vendors libutp at the pinned commit and transfers verified payloads
  in both directions over real UDP sockets. What is *not* tested is a real
  torrent transfer through `anacrolix/torrent`.
- Every measurement in these documents is **on one machine**, over loopback or
  the in-process emulated network. Nothing has run over a real wide-area path.

## Testing

```sh
go test ./...                                  # full suite
go test ./netem/                               # the network harness gate
go test ./native/libutp/                       # interop against real libutp (needs cgo)
go test -race ./...                            # race detector
scripts/check-libutp-reference.sh              # verify the pinned libutp reference
```

`TestManyConcurrentTransfers` runs 1000 concurrent 1 MB transfers by default
and peaks at roughly 5 GB RSS. Set `UTP_TEST_TRANSFERS` to run it smaller —
which is necessary under `-race`.

The interop tests link a vendored copy of libutp and so need cgo and a C++
compiler. With `CGO_ENABLED=0` the package builds as an empty stub and the
rest of the module is unaffected.

## Licence

MIT. libutp, from which behaviour here is derived, is also MIT.
