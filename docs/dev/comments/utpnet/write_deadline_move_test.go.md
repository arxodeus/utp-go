# utpnet/write_deadline_move_test.go

Every comment in `utpnet/write_deadline_move_test.go` at e56c9aa, verbatim.

## Line 14 — func TestMovingTheWriteDeadlineDoesNotRepeatBytes

```
// Moving the write deadline while a Write waits for room neither loses nor
// repeats bytes. Write started over from the beginning whenever the stream
// came back with nothing reported, and the stream reported nothing even
// for bytes it had taken, so the peer got the start of the payload twice.
// From the release review of 9c44c4f (R1).
```

## Line 23 — func TestMovingTheWriteDeadlineDuringWriteBuffers

```
// The same for WriteBuffers, which took the vector write straight through and
// returned "context canceled" once part of it had been taken and the deadline
// moved. Review of 294cc2e, finding 3.
```

## Line 82 — func moveDeadlineDuringWrite

```
// The peer is not reading, so the write is waiting; move its deadline
	// a few times, then let the peer read.
```
