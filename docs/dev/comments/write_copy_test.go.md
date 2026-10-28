# write_copy_test.go

Every comment in `write_copy_test.go` at e56c9aa, verbatim.

## Line 10 — func TestWriteCopiesOnce

```
// A write is copied once. Write copies the caller's bytes into a slice of its
// own, so the caller may reuse its buffer at once; the send buffer then takes
// that slice as it is (sendBuffer.Adopt). It used to copy it again, and a
// 1 MB write allocated 2 MB. libutp copies the application's bytes once
// (utp_internal.cpp:1061).
```
