# recv_buffer_alloc_test.go

Every comment in `recv_buffer_alloc_test.go` at e56c9aa, verbatim.

## Line 9 — func TestReceiveBufferAllocatesAsDataArrives

```
// The receive buffer allocates as data arrives, not its whole capacity up
// front: a connection that has received a kilobyte holds a few, not the
// megabyte it may be advertised.
```

## Line 32 — func TestReceiveBufferMatchesModel

```
// Against a model: packets arrive in order, out of order and repeated, reads
// take random amounts, and what is read is exactly the stream, in order. The
// allocation never passes the capacity but by the one packet taken while
// nothing was readable (receiveBuffer.Admits), and the window and admission
// figures are those of the fixed-size buffer this replaced.
```

## Line 41 — func TestReceiveBufferMatchesModel

```
// wraps during the run
```

## Line 44 — func TestReceiveBufferMatchesModel

```
// every byte sent, in sequence order
```

## Line 47 — func TestReceiveBufferMatchesModel

```
// index of the next payload not yet admitted in order
```

## Line 52 — func TestReceiveBufferMatchesModel

```
// a packet: the next in order, or one a little ahead
```

## Line 64 — func TestReceiveBufferMatchesModel

```
// refused for want of room: the peer would resend
```

## Line 77 — func TestReceiveBufferMatchesModel

```
// a read of any size
```

## Line 117 — func TestReceiveBufferReleasesStorageWhenEmpty

```
// A buffer that grew while busy keeps nothing once it is drained and the
// connection goes quiet: releaseStorage drops the storage when it holds no
// unread bytes, and leaves it when it does. What arrives next is delivered
// as before.
```
