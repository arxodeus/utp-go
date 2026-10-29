# native/libutp/race_on_test.go

Every comment in `native/libutp/race_on_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build race && cgo
```

## Line 5 — const raceEnabled

```
// raceEnabled reports whether the test binary was built with -race. The race
// detector instruments this library and not libutp's C, so a comparison of
// the two's timing measures the detector; those gates report instead.
```
