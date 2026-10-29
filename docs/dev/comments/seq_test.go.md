# seq_test.go

Every comment in `seq_test.go` at e56c9aa, verbatim.

## Line 87 — func TestWrappingLessThan

```
// wrappingLessThan is the sequence-number comparison every fast-retransmit
// decision goes through, so it is tested on its own rather than only through
// a transfer. The wrap cases are the point: a naive `a < b` gets them wrong.
```

## Line 122 — func TestWrappingLessThanIsAntisymmetric

```
// The relation must be antisymmetric everywhere except the two unordered
// cases, across the whole space rather than the handful of points above.
```
