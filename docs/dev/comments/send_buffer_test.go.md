# send_buffer_test.go

Every comment in `send_buffer_test.go` at e56c9aa, verbatim.

## Line 48 — func TestRead

```
// Read of empty buffer returns zero.
```

## Line 65 — func TestRead

```
// Read first chunk of first write.
```

## Line 71 — func TestRead

```
// The rest of the first write, and the second continues it: a read
	// fills its buffer across writes, as libutp fills the last unsent packet
	// (TestSendBufferReadSpansWrites). This test used to expect the read to
	// stop at the end of the first write, which was the defect.
```

## Line 82 — func TestRead

```
// What is left of the second write.
```

## Line 88 — func TestRead

```
// Read with empty buffer returns zero.
```

## Line 112 — func TestSendBufferDoesNotRetainCallerSlice

```
// The buffer must own the bytes it holds. UtpStream.Write returns as soon as
// the data is accepted here, so the caller is free to reuse its array the
// moment it returns -- and io.Writer's contract ("Implementations must not
// retain p") means every stdlib-shaped caller does exactly that.
//
// This buffer used to store the caller's slice by reference. Any full-duplex
// exchange -- read into a buffer, write it back, repeat -- had its queued
// bytes overwritten by the next read.
```

## Line 128 — func TestSendBufferDoesNotRetainCallerSlice

```
// The caller reuses its array, as io.Copy does between iterations.
```

## Line 141 — func TestSendBufferDoesNotRetainOnPartialWrite

```
// The same, for a write the buffer can only take part of: the accepted part
// must be copied too.
```

## Line 163 — func TestSendBufferReadSpansWrites

```
// A read fills its buffer across writes, as libutp fills the last unsent
// packet before starting another (utp_internal.cpp:1013-1023).
```

## Line 182 — func TestSendBufferTakeSharesAWrite

```
// Take hands out a payload lying within one write as that write's own bytes,
// and copies only one that spans two. The shared slice is capped at its end,
// so appending to one payload cannot overwrite the next.
```

## Line 196 — func TestSendBufferTakeSharesAWrite

```
// 400 from the first write, 200 from the second
```
