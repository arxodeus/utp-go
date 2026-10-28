# write_after_close_test.go

Every comment in `write_after_close_test.go` at e56c9aa, verbatim.

## Line 34 — func TestWriteAfterOurFinIsRefused

```
// A write that reaches the connection after this end's FIN has gone -- one
// queued just before Close or CloseWrite -- is refused with an error. It was
// answered with 0 bytes and no error, which an io.Writer may not return: the
// caller was told it succeeded and its bytes were never sent. libutp refuses
// it the same way (utp_internal.cpp:3188).
```

## Line 51 — func TestWriteAfterCleanCloseIsRefused

```
// A write that reaches a connection which closed cleanly, with no error of its
// own, still gets one.
```

## Line 64 — func TestPendingWriterToldWhatWasTaken

```
// A writer part of whose bytes were taken before the connection closed is told
// how many, and that the rest were not.
```
