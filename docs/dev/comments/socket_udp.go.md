# socket_udp.go

Every comment in `socket_udp.go` at e56c9aa, verbatim.

## Line 12 — type udpConn

```
// udpConnReader is what batched reads keep between reads, which differs
	// by platform (batch_read_unix.go, batch_read_other.go). Only the
	// socket's read loop reads, so one is enough.
```

## Line 16 — type udpConn

```
// dfUnsupported records that this platform has no per-packet
	// don't-fragment option, so the MTU search stops asking. See
	// WriteToDontFragment.
```

## Line 40 — func (*udpConn).WriteTo

```
// A peer type this package did not create. Its Hash is by contract the
	// address, which is what utpnet's peerUDPAddr already relies on, so parse
	// that rather than dropping the packet.
	//
	// This used to `return 0, nil`: every packet to such a peer vanished, and
	// the connection above saw a successful send and timed out with nothing to
	// explain it. Silence is the wrong answer whether or not the address
	// parses -- if it does not, that is an error the caller needs.
```

## Line 56 — func (*udpConn).WriteToDontFragment

```
// WriteToDontFragment sends one datagram with fragmentation disabled,
// implementing DontFragmentWriter for a real UDP socket.
//
// The bit is a socket option on every platform that has one at all -- there is
// no per-datagram control message for it -- so it is set, one datagram is
// sent, and it is cleared again. That is safe here because the socket holds
// writeMu exclusively for such a send and shared for every other one, so no
// other write can land between the two. See Socket.writeDatagram.
//
// It is cleared even when the send fails. Leaving it set would make every
// subsequent datagram on this socket unfragmentable, which is the one thing
// libutp is explicit about not wanting: ordinary data has to stay
// fragmentable, "now we need it to fragment just to get it through"
// (utp_internal.cpp:898-905).
//
// A platform with no such option answers ErrDontFragmentUnsupported, and
// nothing is sent. The caller then sends it normally.
```

## Line 78 — func (*udpConn).WriteToDontFragment

```
// Remember it, so a socket on a platform without the option is not
		// asked once per probe for the life of the process.
```

## Line 85 — func (*udpConn).WriteToDontFragment

```
// The datagram went out, but the socket is now in a state where every
		// later one would too. Report it rather than carrying on quietly.
```

## Line 97 — func (*udpConn).LocalAddr

```
// LocalAddr returns the address this connection is bound to.
```

## Line 105 — type incomingPacketRaw

```
// borrowed says payload is the reader's buffer, which it reuses once the
	// datagram has been dispatched: the decoded packet copies its body out.
```

## Line 110 — type datagram

```
// datagram is one datagram from a batched read.
```

## Line 114 — type datagram

```
// borrowed says payload is reused by the next batch. See
	// incomingPacketRaw.borrowed.
```

## Line 119 — type batchReader

```
// batchReader is a Conn that can hand over everything already queued in one
// call. udpConn on Unix is one; anything else reads a datagram at a time.
```

## Line 125 — type QueuedReader

```
// QueuedReader is a Conn that can say how many datagrams ReadFrom would return
// without waiting. The socket reads those before it acknowledges anything, so
// a run of datagrams that were already waiting draws one acknowledgement, not
// one each: libutp's embedder reads until the socket would block and only then
// calls utp_issue_deferred_acks (utp.h:512-517). A Conn that implements
// neither this nor batched reads has every datagram treated as a batch of its
// own, which holds nothing back.
//
// Queued may count low, never high: a datagram it counts must be one ReadFrom
// returns at once.
```

## Line 139 — const maxQueuedRun

```
// maxQueuedRun bounds how many datagrams a QueuedReader's run may hold an
// acknowledgement for, as maxReadBatch does a batched read.
```
