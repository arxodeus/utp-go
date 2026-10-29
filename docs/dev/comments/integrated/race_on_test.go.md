# integrated/race_on_test.go

Every comment in `integrated/race_on_test.go` at e56c9aa, verbatim.

## Line 1 — package integrated

```
//go:build race
```

## Line 5 — const raceEnabled

```
// raceEnabled reports whether the test binary was built with -race.
// The race detector slows this package's transfers down by roughly an order
// of magnitude, so time budgets are scaled rather than left to fail
// spuriously.
```
