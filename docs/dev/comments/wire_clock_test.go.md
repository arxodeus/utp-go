# wire_clock_test.go

Every comment in `wire_clock_test.go` at e56c9aa, verbatim.

## Line 9 — func TestWireClockIsMonotonic

```
// The wire clock must not jump when the wall clock is stepped. A test cannot
// step the host's clock, so this checks the two things the guarantee rests
// on: the epoch carries Go's monotonic reading, which time.Since uses and an
// NTP step does not move, and while nothing steps the clock the stamps agree
// with the wall clock they replaced.
```

## Line 23 — func TestWireClockIsMonotonic

```
// Strictly not backwards across consecutive reads.
```
