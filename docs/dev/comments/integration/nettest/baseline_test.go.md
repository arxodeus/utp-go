# integration/nettest/baseline_test.go

Every comment in `integration/nettest/baseline_test.go` at e56c9aa, verbatim.

## Line 10 — func TestTCPBaseline

```
// A control, run in the same process and the same container as the suite
// above: the same eleven subtests against kernel TCP over loopback.
//
// It exists because "the suite passes" is not the only thing worth knowing
// from it. The subtest timings say what the deadline paths cost, and a timing
// is meaningless without something to compare it to -- a machine that is slow
// at all of this would otherwise read as a library that is slow at it.
```
