# temporary_error_windows.go

Every comment in `temporary_error_windows.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build windows
```

## Line 12 — func isPacketTooBig

```
// isPacketTooBig reports whether err is WSARecvFrom's WSAEMSGSIZE: a
// datagram that did not fit the buffer, of which Windows returns nothing.
```
