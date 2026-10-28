# integration/anacrolix/doc.go

Every comment in `integration/anacrolix/doc.go` at e56c9aa, verbatim.

## Line 1 — package anacrolix

```
// Package anacrolix checks, at compile time and at run time, that this
// library can stand in for a uTP implementation in
// github.com/anacrolix/torrent.
//
// It is a separate Go module on purpose. The interface being satisfied is
// unexported, so proving the claim means importing the torrent package, and
// that pulls in a large dependency tree. Keeping it here means the library's
// own go.mod stays small: nothing a caller depends on grows because this
// check exists.
//
// Run it with:
//
//	cd integration/anacrolix && go test ./...
```
