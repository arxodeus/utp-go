# write_lock_test.go

Every comment in `write_lock_test.go` at e56c9aa, verbatim.

## Line 11 — func TestAWriteWaitingForAnotherHonoursItsContext

```
// A write waiting behind another one ends when its own context does. The
// writes were serialised by a mutex, so one queued behind a writer the peer's
// window held up waited however long that took. From the review of 294cc2e
// (finding 1); the already-cancelled case is that review's proposed test.
```

## Line 22 — func TestAWriteWaitingForAnotherHonoursItsContext

```
// The peer is not reading, so this one waits, holding the write lock.
```

## Line 45 — func TestAWriteWaitingForAnotherHonoursItsContext

```
// The first write is unaffected, and the others sent nothing.
```
