# netem/close_without_reading_test.go

Every comment in `netem/close_without_reading_test.go` at e56c9aa, verbatim.

## Line 11 — func TestCloseWithoutReadingDoesNotHang

```
// Closing from a consumer that never read must not hang.
//
// UtpStream.Close waits for the connection goroutine to finish, and that
// goroutine's shutdown path hands bytes to the reader. When that handoff could
// block, a consumer that accepted a stream, never read it, and then closed it
// deadlocked against itself: Close waited for the loop, the loop waited for the
// reader, and the stream context that would break the tie is cancelled by Close
// only after its wait returns.
//
// libutp has no equivalent. utp_call_on_read hands the embedder its bytes and
// returns; a slow application closes the receive window and never stops the
// protocol.
```

## Line 78 — func TestCloseWithoutReadingDoesNotHang

```
// Let the sender fill the receive buffer and the read queue behind it.
```
