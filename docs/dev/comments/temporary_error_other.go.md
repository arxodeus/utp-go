# temporary_error_other.go

Every comment in `temporary_error_other.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build !windows
```

## Line 5 — func isPacketTooBig

```
// isPacketTooBig is Windows' alone: elsewhere a datagram larger than the
// buffer is truncated, not an error.
```
