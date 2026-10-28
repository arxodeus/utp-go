# borrowed_decode_test.go

Every comment in `borrowed_decode_test.go` at e56c9aa, verbatim.

## Line 8 — func TestDecodedPacketOutlivesItsBuffer

```
// The socket decodes each datagram from its read buffer and reuses the buffer
// for the next read. A packet processed before then needs nothing of its own;
// one that is kept takes its body (own), and nothing else in it refers to the
// buffer: the header is decoded into values and the extensions copied. Here
// the buffer is overwritten after own, as the next read would, and the packet
// must be unchanged.
```

## Line 37 — func TestOutOfOrderDataOutlivesItsBuffer

```
// Data held out of order outlives the read that brought it, so the receive
// buffer keeps a copy: the body may be the socket reader's buffer.
```
