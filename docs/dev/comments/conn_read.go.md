# conn_read.go

Every comment in `conn_read.go` at e56c9aa, verbatim.

## Line 11 — type readOrWriteResult

```
// pooled marks a chunk of received data from readChunks, which the
	// reader returns once it has copied it out. See newReadChunk.
```

## Line 16 — var readChunks

```
// readChunks holds the chunks received data is handed to the reader in.
//
// Each chunk was a new buffer and a new result: two allocations for every
// packet's worth of data read. The reader copies a chunk out and is done with
// it, so it comes back here.
```

## Line 23 — const pooledReadChunkMin

```
// pooledReadChunkMin is the least a chunk must hold to come from readChunks.
// A pooled buffer is as large as the largest packet, and a reader that falls
// behind leaves up to a read queue's worth of chunks waiting: small ones are
// sized to their data, so that a trickle of a few bytes at a time does not
// hold a full packet's memory for each.
```

## Line 30 — func newReadChunk

```
// newReadChunk is a result holding n bytes of received data, for the reader.
// size is the most any chunk on this connection holds.
```

## Line 43 — func (*readOrWriteResult).release

```
// release returns a chunk the reader has finished with to readChunks.
```

## Line 60 — func (*connection).processReads

```
// Drain contiguous data even once the connection is closed: bytes that
	// arrived before teardown are still owed to the reader.
	//
	// Never block the event loop waiting for the application to read. libutp
	// does not: utp_call_on_read hands the embedder its bytes and returns, and
	// a slow application is handled by the advertised receive window shrinking
	// (utp_call_get_read_buffer_size), not by libutp stopping. This connection
	// advertises RecvBuf.Window(), so leaving unread bytes in the receive
	// buffer is exactly that backpressure.
	//
	// Every sender on c.reads holds mu, so a free slot observed here is
	// still free at the send below: consumers only ever remove.
```

## Line 78 — func (*connection).processReads

```
// Size the buffer to the data, not to the largest packet this
		// connection might carry.
		//
		// This allocated MaxPacketSize every time and handed the whole thing to
		// the reader with a separate length, so a 40-byte chunk kept 1400 bytes
		// alive until the reader dropped it. IsEmpty() is also true of a buffer
		// holding only out-of-order bytes, which are not readable yet; that
		// case used to allocate, read nothing, and break.
```

## Line 108 — func (*connection).processReads

```
// If we have reached eof, hand the reader the end-of-stream marker -- but
	// only once everything already received has been handed over first.
	//
	// eof() means every byte the peer sent has *arrived*, not that the reader
	// has been given it. While the drain above ran to completion that
	// distinction did not exist. Now that it can stop early with bytes still
	// in the receive buffer, announcing the end here truncates the transfer:
	// the reader sees end-of-stream and stops, and the rest is discarded.
	// Measured, when this guard was missing: 409308 bytes delivered out of
	// 524288 against real libutp.
```

## Line 121 — func (*connection).processReads

```
// Bytes ready for a reader whose queue is full wait for it to make room,
	// and only then can a read change anything here: it is what reopens the
	// receive window. See UtpStream.notifyRead.
```

## Line 129 — func (*connection).drainable

```
// drainable is how many bytes processReads would hand the reader now: what is
// contiguous in the receive buffer, up to a chunk of at most MaxPacketSize for
// each free slot in the reader's queue -- processReads' own loop, counted.
```

## Line 143 — func (*connection).recvWindow

```
// recvWindow is the receive window an outgoing packet advertises: the
// buffer's, plus what this pass will hand the reader after the packet goes
// (drainCredit). It never exceeds the buffer: the credit is bytes the buffer
// holds.
```

## Line 151 — func (*connection).onReadDrained

```
// onReadDrained tells the peer when handing bytes up has reopened a receive
// window smaller than the one now available.
//
// libutp's utp_read_drained (utp_internal.cpp:3242-3261), which its embedder
// calls whenever it has drained the socket:
//
//	const size_t rcvwin = conn->get_rcv_window();
//	if (rcvwin > conn->last_rcv_win) {
//	    if (conn->last_rcv_win == 0) conn->send_ack();
//	    else conn->schedule_ack();
//	}
//
// Without it the peer finds out only from the next acknowledgement this end
// happens to send -- and while it is blocked on a window too small to fit a
// packet it sends nothing, so nothing draws one. The test is growth, not
// growth from zero: a window too small to use is as blocking as a closed
// one, and libutp's condition covers both because it compares against what
// was actually last sent.
//
// libutp's distinction between acknowledging now and scheduling is not
// preserved, because it does not exist here: flushAck runs at the end of this
// same pass either way. History: HISTORY.md, "connection.onReadDrained".
```

## Line 183 — func (*connection).drainReadsForTeardown

```
// drainReadsForTeardown hands over everything left in the receive buffer as the
// connection ends.
//
// The ordinary drain in processReads gives up when the read queue is full and
// waits to be woken, which is what keeps the event loop from blocking on the
// application. That is not good enough here: this is the last pass, the receive
// buffer is about to be closed, and anything not handed over now is lost. A
// truncated transfer is worse than a slow one -- when this path merely called
// processReads, a large loopback transfer arrived short and
// TestUdpTransfer failed on the payload comparison.
//
// So this one waits for room, and the wait has two escapes: the connection's
// context, and the consumer closing the stream. The second is what keeps the
// deadlock from coming back. A consumer that closed without reading is not
// owed these bytes, and waiting for it to take them would be waiting forever,
// because UtpStream.Close is itself blocked waiting for this goroutine.
```

## Line 224 — func (*connection).deliverTerminalRead

```
// deliverTerminalRead hands the reader the single end-of-stream marker: a
// result with an empty Data slice, which is what UtpStream.ReadToEOF treats as
// the end of the stream. It is delivered at most once.
//
// Every path that closes the connection must reach this, including idle
// timeout, RESET and a locally-initiated close. A reader blocks on c.reads
// forever if the marker never arrives.
```

## Line 236 — func (*connection).deliverTerminalRead

```
// A clean end-of-stream reports no error, matching io.ReadAll: ReadToEOF
	// reads to EOF by definition, so reaching it is success. Only an abnormal
	// close (RESET, idle timeout) carries an error. See readEndErr.
```

## Line 242 — func (*connection).deliverTerminalRead

```
// Published before delivery is attempted, so the reason the stream ended
	// is available however the reader finds out it has.
```

## Line 246 — func (*connection).deliverTerminalRead

```
// Never block the event loop on this either. UtpStream.Close waits for
	// this goroutine to finish, and only the reader drains c.reads, so a
	// consumer that stopped reading and then closed deadlocked against
	// itself: Close waited for the loop, the loop waited for the reader, and
	// the context that would break the tie is cancelled by Close only after
	// its wait returns.
	//
	// If there is no room the marker is simply not sent. Closing c.reads when
	// this loop exits reports the end of the stream just as well, and carries
	// the error through terminalErr.
```

## Line 259 — func (*connection).deliverTerminalRead

```
// let a later pass deliver it if room appears
```

## Line 264 — func (*connection).readEndErr

```
// readEndErr is what the reader is told when the stream ends: the error that
// ended the connection, or nil for a clean end -- and a clean end means the
// reader has been given everything the peer sent, up to its FIN. A
// connection that ended without error but short of that reports
// ErrReadClosed: this end closed before it had read everything.
//
// Once everything the peer sent before its FIN has arrived and been handed
// on, the read side has ended cleanly, whatever ends the connection
// afterwards. libutp has delivered every byte to its embedder by then and
// signalled UTP_STATE_EOF (utp_internal.cpp:2339-2343, :2355-2357); a RESET
// that follows is reported as an error but takes nothing back, and
// go-libutp's reader returns the data and io.EOF before it (go-libutp's
// conn.go, readNoWait). It happens: the side that closed first destroys
// its connection once its FIN is acknowledged, and answers whatever the
// other side sends next -- a window update as its reader drains -- with a
// RESET (:2179-2182, :2907-2944). History: HISTORY.md, "readEndErr".
```

## Line 290 — func (*connection).receivedThroughFin

```
// receivedThroughFin reports whether every byte the peer sent before its FIN
// has arrived. eof() asks the same of a connection still open.
```

## Line 298 — type terminalError

```
// terminalError boxes the end-of-stream error so it can be stored atomically,
// including the nil case that means a clean end of stream.
```

## Line 302 — func (*connection).sendRead

```
// sendRead delivers a read result, giving up if the connection context is
// cancelled. Without the context arm a reader that has already gone away
// wedges the event loop permanently.
```
