# netem/race_on_test.go

Every comment in `netem/race_on_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build race
```

## Line 5 — const raceEnabled

```
// raceEnabled reports whether the test binary was built with -race. The race
// detector instruments this library and not libutp's C, and slows the
// emulated network's Go side by an order of magnitude; a test whose reference
// is libutp's timing over it measures the detector.
```
