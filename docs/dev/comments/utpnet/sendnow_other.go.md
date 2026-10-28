# utpnet/sendnow_other.go

Every comment in `utpnet/sendnow_other.go` at e56c9aa, verbatim.

## Line 1 — package utpnet

```
//go:build !unix
```

## Line 7 — type rawSender

```
// rawSender has no single non-blocking attempt here, so every datagram goes
// to the writer. See sendnow_unix.go.
```
