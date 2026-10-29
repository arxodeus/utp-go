# integration/nettest/doc.go

Every comment in `integration/nettest/doc.go` at e56c9aa, verbatim.

## Line 1 — package nettest

```
// Package nettest runs the Go standard library's own net.Conn conformance
// suite against this library's net.Conn.
//
// It is a separate Go module on purpose, for the same reason the anacrolix
// one is: the library's own go.mod stays small, and nothing a caller depends
// on grows because this check exists.
//
// Run it with:
//
//	cd integration/nettest && go test ./...
//
// Run it under the race detector, which is what the suite is written for:
//
//	cd integration/nettest && go test -race -count=1 ./...
```
