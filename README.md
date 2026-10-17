# utp

A pure-Go implementation of the µTorrent Transport Protocol (uTP). No cgo.
It behaves as libutp, the reference implementation, does, and every place it
differs is recorded and measured in [DEVIATIONS.md](DEVIATIONS.md).

```
go get github.com/arxodeus/utp
```

```go
import "github.com/arxodeus/utp"            // the protocol: UtpSocket, UtpStream
import "github.com/arxodeus/utp/utpnet"     // as net.PacketConn and net.Conn
```

For a program that uses `anacrolix/go-libutp`, `compat/golibutp` is go-libutp's
API on this library; see [compat/golibutp/README.md](compat/golibutp/README.md).

## Where it came from

It began as a fork of [`zen-eth/utp-go`](https://github.com/zen-eth/utp-go),
aimed at a uTP implementation a BitTorrent client can rely on and that builds
with `CGO_ENABLED=0`. So little of that remains unchanged that it is now its
own library, under its own module path; [UPSTREAM.md](UPSTREAM.md) records
what could still go back.

Upstream's own integration tests could not pass on a clean checkout. They now
do. See:

| Document | What it covers |
| --- | --- |
| [COMPATIBILITY.md](COMPATIBILITY.md) | What has been checked against libutp, how strong the evidence for each area is, and what has not been checked at all. |
| [CONFORMANCE.md](CONFORMANCE.md) | The conformance corpus: how two implementations are compared packet by packet, what it found, and what it does not cover. |
| [HARNESS.md](HARNESS.md) | The emulated network harness: what it models, what it measures, and the numbers proving it is sound. |
| [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) | **Read this first.** Root cause of every defect fixed, what has and has not been measured, and everything found but not fixed. |
| [integration/anacrolix/README.md](integration/anacrolix/README.md) | Using this library with `anacrolix/torrent`: what is checked, and how to wire it in. |
| [compat/golibutp/README.md](compat/golibutp/README.md) | A drop-in replacement for `anacrolix/go-libutp`: one `replace` line in an application's go.mod, and everything that imports go-libutp -- `anacrolix/torrent` included -- runs on this library. |
| [FUZZING.md](FUZZING.md) | The fuzz targets and soak tests: what each asserts, what they found, and what they do not cover. |
| [BENCHMARKS.md](BENCHMARKS.md) | Congestion-control measurements: LEDBAT against LEDBAT++, the latecomer experiment, and what the numbers do not prove. |
| [REFERENCE.md](REFERENCE.md) | Which copy of libutp is normative, and proof the copy used for differential testing is protocol-identical to it. |
| [DEVIATIONS.md](DEVIATIONS.md) | Every intentional difference from libutp. |
| [HISTORY.md](HISTORY.md) | The long reasoning behind particular pieces of code -- the defect each guards against, how it was found and measured -- moved out of the comments, which point to it. |
| [RELEASING.md](RELEASING.md) | The release gates, and the order in which the two modules are tagged. |
| [UPSTREAM.md](UPSTREAM.md) | What should go back to `zen-eth/utp-go`, and in what order. |
| [native/libutp/VENDOR.md](native/libutp/VENDOR.md) | The vendored libutp: provenance, what was taken, and how the bridge works. |

Two things to be aware of before depending on this:

- **Interoperability is tested against three other implementations** --
  real libutp (`native/libutp/`, vendored at the pinned commit), go-utp's
  pure Go port of it (`integration/goutp/`), and `anacrolix/torrent`
  (`integration/anacrolix/`, a hash-verified torrent transfer) -- with
  verified payloads in both directions, over real UDP sockets and over links
  that lose, reorder and duplicate packets.
- Every measurement in these documents is **on one machine**, over loopback or
  the in-process emulated network. Nothing has run over a real wide-area path.

## Testing

```sh
go test ./...                                  # full suite
go test ./netem/                               # the network harness gate
go test ./native/libutp/                       # interop against real libutp (needs cgo)
(cd integration/goutp && go test ./...)        # interop against go-utp's pure Go engine
go test -race ./...                            # race detector
scripts/check-libutp-reference.sh              # verify the pinned libutp reference
```

The measurement suites -- the congestion-control benchmarks and the libutp
comparison -- run once each by default, which is enough to gate them: every
assertion they make is per-run. To take a measurement rather than check for a
regression, ask for repeats, and give it a longer timeout:

```sh
UTP_BENCHMARK_REPEATS=7 go test -timeout 60m ./netem/
```

That is where the tables in [BENCHMARKS.md](BENCHMARKS.md) come from. Medians
of seven, because the loss profiles are bimodal -- one retransmission timeout
dominates a short transfer -- and fewer runs cannot separate the two modes.

`TestManyConcurrentTransfers` runs 1000 concurrent 1 MB transfers by default
and peaks at about 3.2 GB RSS (3.17-3.33 GB, three runs), nearly all of it
the transfers' data: each write's copy, each receive buffer, each read result. Set `UTP_TEST_TRANSFERS` to run it smaller —
which is necessary under `-race`.

The interop tests link a vendored copy of libutp and so need cgo and a C++
compiler. With `CGO_ENABLED=0` the package builds as an empty stub and the
rest of the module is unaffected: its tests still build and run, apart from the
ones that compare against libutp.

## Licence

MIT. libutp, from which behaviour here is derived, is also MIT.
