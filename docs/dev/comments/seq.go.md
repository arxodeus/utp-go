# seq.go

Every comment in `seq.go` at e56c9aa, verbatim.

## Line 3 — type circularRangeInclusive

```
// circularRangeInclusive represents a range bounded inclusively below and above that supports wrapping arithmetic.
```

## Line 10 — func newCircularRangeInclusive

```
// newCircularRangeInclusive returns a new circularRangeInclusive.
```

## Line 19 — func (*circularRangeInclusive).Start

```
// Start returns the start of the range (inclusive).
```

## Line 24 — func (*circularRangeInclusive).End

```
// End returns the end of the range (inclusive).
```

## Line 29 — func (*circularRangeInclusive).Contains

```
// Contains returns true if item is contained in the range.
```

## Line 40 — func (*circularRangeInclusive).Next

```
// Next returns the next item in the range, or nil if the range is exhausted.
```

## Line 55 — func wrappingLessThan

```
// wrappingLessThan reports whether a precedes b in the uint16 sequence space,
// where "precedes" means the forward distance from a to b is less than half
// the space. It is the analogue of libutp's `wrapping_compare_less`
// (utp_utils.cpp), which this fork needs wherever two sequence numbers are
// ordered without knowing which side of a wrap either is on.
//
// Equal values are not less than one another, and a pair exactly half the
// space apart is not ordered either -- there is no correct answer for
// (0, 32768), so it reports false in both directions rather than pretending.
```
