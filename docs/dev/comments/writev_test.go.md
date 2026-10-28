# writev_test.go

Every comment in `writev_test.go` at e56c9aa, verbatim.

## Line 12 — func writevPair

```
// writevPair brings up a connected pair over loopback and returns both ends.
```

## Line 50 — func TestWriteVDeliversTheConcatenation

```
// A vectored write, libutp's utp_writev: several buffers written as one stream
// write.
//
// What must be true is that the peer sees exactly the concatenation, with
// nothing lost between the pieces and no framing implied by the split -- uTP
// is a byte stream, and a caller that split its data into a header and a body
// must not be able to tell afterwards how it split it.
```

## Line 62 — func TestWriteVDeliversTheConcatenation

```
// Sizes chosen to straddle a packet boundary, so the pieces cannot line
	// up with packets: a 1400-byte path carries about 1380 bytes of payload.
```

## Line 110 — func TestWriteVMatchesASingleWrite

```
// A vectored write is indistinguishable on the wire from the same bytes
// written whole. Asserted because the saving it exists for is a copy, not a
// change in what is sent, and a framing difference would be a bug rather than
// an optimisation.
```

## Line 122 — func TestWriteVMatchesASingleWrite

```
// An awkward split: not on any packet boundary.
```

## Line 160 — func TestWriteVEdges

```
// The edges: nothing to write, and a write after the sending side is closed.
```

## Line 182 — func TestWriteVDoesNotRetainCallerBuffers

```
// WriteV must not retain the caller's buffers, for the same reason Write must
// not: a write that hit its deadline leaves its entry in the connection's
// pending list, and a caller is free to reuse its buffers the moment the call
// returns. Run this under -race.
```

## Line 208 — func TestWriteVDoesNotRetainCallerBuffers

```
// Reuse immediately, as a real caller would.
```

## Line 219 — func benchmarkVectored

```
// What WriteV is actually worth, measured rather than asserted.
//
// The claim is one copy rather than two: a caller with several buffers either
// joins them (allocate the total, copy into it) and calls Write (which
// allocates the total again and copies again), or calls WriteV (allocate
// once, copy once). Allocated bytes per operation is the figure that shows
// it, and it is far more robust to a noisy loopback than nanoseconds are.
//
// Run both together -- `go test -bench 'WriteV|WriteJoined' -benchmem` -- and
// compare B/op. Cross-run comparisons in this harness are not worth having;
// see BENCHMARKS.md.
```
