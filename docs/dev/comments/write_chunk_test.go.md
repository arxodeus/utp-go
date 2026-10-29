# write_chunk_test.go

Every comment in `write_chunk_test.go` at e56c9aa, verbatim.

## Line 10 — func TestALargeWriteHoldsOneChunk

```
// A write larger than the send buffer is not copied whole before any of it
// can be taken: what it holds beyond the send buffer is at most one send
// buffer's worth. It copied everything first, so a 32 MB write to a peer not
// reading held 32 MB. From the release review of 9c44c4f.
```

## Line 24 — func TestALargeWriteHoldsOneChunk

```
// the peer is not reading: the write waits
```

## Line 34 — func TestALargeWriteHoldsOneChunk

```
// at most a chunk each, not yet taken by the loop
```

## Line 41 — func TestConcurrentLargeWritesAreNotInterleaved

```
// Writes of several chunks, made at once from two goroutines, each arrive
// whole: a write's chunks are not interleaved with another's.
```
