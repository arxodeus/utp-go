# native/libutp/debuglog.go

Every comment in `native/libutp/debuglog.go` at e56c9aa, verbatim.

## Line 1 — package libutp

```
//go:build cgo && libutp_debuglog
```

## Line 5

```
// Built with the libutp_debuglog tag, libutp is compiled with its debug log
// lines, so that Driver.EnableWindowLog can read the congestion window from
// them. See there for why it is not the default.
```

## Line 9

```
// #cgo CXXFLAGS: -DUTP_DEBUG_LOGGING=1
```
