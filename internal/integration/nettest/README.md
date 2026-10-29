# net.Conn conformance

A separate Go module that runs the `net.Conn` conformance suite from
`golang.org/x/net/nettest` against [`utpnet.Conn`](../../../utpnet). It is
kept apart so that the library's own go.mod stays small.

```sh
cd internal/integration/nettest && go test -race -count=5 ./...
```

`TestUTPConnSatisfiesNetConn` runs the suite: concurrent reads and writes,
deadlines in the past and moved while blocked, and `Close` during `Read`.
The suite notes that some failures appear only across repeated runs, hence
`-count=5`.

`TestTCPBaseline` runs the same subtests against kernel TCP over loopback,
as a reference for their timings.

The suite covers `net.Conn` only, not `net.PacketConn` or `net.Listener`,
and nothing specific to uTP.
