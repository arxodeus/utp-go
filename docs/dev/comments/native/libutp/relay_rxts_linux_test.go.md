# native/libutp/relay_rxts_linux_test.go

Every comment in `native/libutp/relay_rxts_linux_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo && linux
```

## Line 12 — func enableRxTimestamps

```
// enableRxTimestamps asks the kernel to stamp each datagram c receives with
// the time it arrived (SO_TIMESTAMPNS).
```

## Line 22 — func rxTimestamp

```
// rxTimestamp is the arrival time the kernel attached to a datagram, if any.
```
