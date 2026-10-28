# read_lock_test.go

Every comment in `read_lock_test.go` at e56c9aa, verbatim.

## Line 11 — func TestAReadWaitingForAnotherHonoursItsContext

```
// A read waiting behind another one ends when its own context does. Reads
// were serialised by a mutex, so one queued behind a reader waiting on a quiet
// stream waited as long as that one did, whatever its own context said.
// Release review of 224c363, finding 2.
```

## Line 20 — func TestAReadWaitingForAnotherHonoursItsContext

```
// Nothing has been sent, so this one waits, holding the read lock.
```

## Line 44 — func TestAReadWaitingForAnotherHonoursItsContext

```
// The first read is unaffected, and gets what is sent.
```

## Line 58 — func TestAReadWaitingForAnotherEndsAtCloseRead

```
// A read waiting for another gives up when the read side is closed, rather
// than waiting to be let in and told so.
```
