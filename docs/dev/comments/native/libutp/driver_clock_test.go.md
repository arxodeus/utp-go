# native/libutp/driver_clock_test.go

Every comment in `native/libutp/driver_clock_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 12 — func TestDriverClockOutlivesTheDriver

```
// The driver's clock can be read after the driver is closed, and from another
// goroutine while it moves. A connection given it as its clock reads it from
// its own goroutine, and goes on after its test has closed the driver; that
// read went to freed memory and crashed the fuzzer's workers.
```
