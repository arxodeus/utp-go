# netem/close_timing_test.go

Every comment in `netem/close_timing_test.go` at e56c9aa, verbatim.

## Line 14

```
// What Close waits for, measured on a link whose conditions can be changed.
//
// Close used to wait for the connection's event loop to exit, full stop. A
// loop with unacknowledged data does not exit until its retransmission ladder
// runs out -- 1 + 2 + 4 + 8 + 16 seconds -- and where that is not enough, the
// 60-second idle timeout behind it. Closing a connection whose peer had gone
// took 31 seconds in one shape and 60 in another, with the caller held for all
// of it. The standard library's net.Conn suite found it: the eleven subtests
// that kernel TCP finishes in 0.43s took 140 seconds here, and almost all of
// that was Close.
//
// The obvious fix, a flat timeout, breaks the opposite case. Write returns
// once the data is in the send buffer rather than once it is acknowledged
// (conn_send.go, processWrites), so a write followed by a close has a tail still to
// flush -- and on a slow link that tail takes longer than any timeout short
// enough to be worth having. A clock cannot tell the two apart. Whether the
// peer is still answering can, and that is what Close now watches.
//
// These two tests hold each end of that. Both need a link that can be changed
// underneath a live connection, which is why they are here and not beside the
// socket: on loopback the tail flushes in milliseconds and a closed peer
// politely sends a FIN, so neither case can be staged at all. Written against
// real sockets first, both passed while measuring nothing -- Close returned in
// 8ms with nothing left to send, and in 0s against a "vanished" peer that had
// in fact said goodbye.
```

## Line 40 — func TestCloseFlushesASlowTail

```
// A close on a link too slow to have flushed must still deliver every byte.
//
// The link carries 2 Mbps, so the send buffer's worth of tail takes seconds to
// drain -- comfortably past closeStallTimeout. A flat bound would cut it off
// here, and the peer would be short exactly the bytes Close stopped waiting
// for.
```

## Line 101 — func TestCloseFlushesASlowTail

```
// The stream's Close returns at once, as libutp's utp_close does; the
	// socket's Close is where the wait is (UtpSocket.awaitLingering).
```

## Line 129 — func TestCloseFlushesASlowTail

```
// The point of the measurement: this is well past any flat bound that
	// would also have fixed the vanished-peer case.
```

## Line 140 — func TestCloseDoesNotWaitForASilentPeer

```
// A close whose peer has gone silent must return promptly.
//
// The link is set to drop everything in both directions before the close, so
// nothing answers and nothing refuses either: no acknowledgement, no FIN, no
// RESET. That is what a peer that was unplugged looks like, and it is the case
// that cost 31 seconds.
```

## Line 192 — func TestCloseDoesNotWaitForASilentPeer

```
// Unplug it. Both directions, so nothing the peer might say gets back
	// either -- a closed socket would have sent a FIN, which is a different
	// case entirely and the one this test kept accidentally measuring.
```

## Line 213 — func TestCloseDoesNotWaitForASilentPeer

```
// Unbounded, this waits out the retransmission ladder (1+2+4+8+16 = 31s)
	// and, when the connection has not given up by then, the 60-second idle
	// timeout behind it -- measured at 60.001s with the bound removed. Two
	// seconds of silence plus whatever the last exchange was still doing is
	// the bound; anything near either of those numbers means it is gone.
```

## Line 222 — func TestCloseDoesNotWaitForASilentPeer

```
// And it should not be instant either: there was unacknowledged data, so
	// Close had something to wait for and did wait, rather than returning
	// because the connection had already finished.
```

## Line 234 — const streamCloseBound

```
// streamCloseBound is how long a stream's Close may take: it waits for
// nothing, so this is scheduling.
```
