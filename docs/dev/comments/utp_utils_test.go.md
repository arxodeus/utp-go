# utp_utils_test.go

Every comment in `utp_utils_test.go` at e56c9aa, verbatim.

## Line 10 — func TestRandomUint16DataRace

```
// test for data race
```

## Line 39 — func TestWrappingSubUint32

```
// A "later" value that is numerically smaller is the normal case
		// after a wrap, not an error: the difference is still small.
```

## Line 53 — func TestWrappingSubUint32AcrossEveryOffset

```
// The difference of any two uint32 timestamps taken within one wrap period
// must be the true elapsed value, wherever the wrap point falls between them.
```

## Line 58 — func TestWrappingSubUint32AcrossEveryOffset

```
// wraps naturally
```

## Line 92 — func TestDurationBetween

```
// Across the wrap: earlier is just below the boundary, later just above.
```

## Line 101 — func TestRandomUint16CoversTheWholeRange

```
// Connection ids and sequence numbers are drawn from all 65536 values, as
// libutp draws them. 65535 used to be unreachable. Two million draws miss a
// given value with probability e^-30.
```
