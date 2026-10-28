# stream.go

Every comment in `stream.go` at e56c9aa, verbatim.

## Line 16 — var ErrNotConnected, ErrReadClosed, ErrSocketClosed

```
// ErrReadClosed is returned by Read after CloseRead. The connection is
	// still alive and still sending; this end has simply stopped taking
	// delivery.
```

## Line 20 — var ErrNotConnected, ErrReadClosed, ErrSocketClosed

```
// ErrSocketClosed is what a connection reports once the socket it ran on
	// has been closed under it. It is a net.ErrClosed, as a read from a
	// closed Go socket is.
```

## Line 26 — type UtpStream

```
// UtpStream is one uTP connection, as UtpSocket's Connect and Accept return
// it.
//
// Its lifetime is the socket's, not the call's that made it: cancelling a
// dial's or an accept's context after it returns does not end the stream.
// Read and Write each take a context that governs that one call. CloseWrite
// sends a FIN once what is written has gone, CloseRead stops delivery, and
// Close does both and returns at once, the connection delivering what it
// holds in the background; UtpSocket.Close waits for that, while the peer
// still answers. Done is closed when the connection has ended.
```

## Line 37 — type UtpStream

```
// socketCtx is the context of the socket the stream runs on; streamCtx
	// is the stream's own, which ends with it. See endWait.
```

## Line 48 — type UtpStream

```
// writeClosed is set by CloseWrite: this end has finished sending, but is
	// still reading. Distinct from shutdown, which means finished entirely.
```

## Line 51 — type UtpStream

```
// readClosed is set by CloseRead: this end has finished reading, but is
	// still sending. The mirror of writeClosed, and libutp's read_shutdown.
```

## Line 54 — type UtpStream

```
// readShutCh is closed by CloseRead, to wake a Read already waiting.
	// Made by the first read that waits (readClosedSignal), so a stream
	// never read from carries none. Guarded by readShutMu.
```

## Line 59 — type UtpStream

```
// readBusy is set while a read is in progress, and readWake, made only
	// by a read that has to wait for it, is closed when it ends. Guarded by
	// readShutMu. See lockRead.
```

## Line 64 — type UtpStream

```
// writeSem holds one write's chunks together: a write holds it while it
	// is queued, and waits for it under its own context. See lockWrite.
```

## Line 69 — type UtpStream

```
// linger, set by the socket that made this stream, takes it over at
	// Close. See UtpSocket.linger.
```

## Line 73 — type UtpStream

```
// abandoned is closed by Close, telling the connection that whatever is
	// still buffered for this reader is owed to nobody.
```

## Line 76 — type UtpStream

```
// readRemainder holds bytes from a chunk that a Read call could not fit
	// in the caller's buffer. Guarded by the read lock (lockRead).
```

## Line 79 — type UtpStream

```
// readErr is the terminal read error, kept so every Read after the first
	// one returns it rather than blocking. Guarded by the read lock.
```

## Line 82 — type UtpStream

```
// ended is closed once the connection's event loop has returned, after
	// it has published terminalErr. Nothing serves writes after that, so a
	// writer must hear of it from here.
```

## Line 88 — const readQueueLen

```
// readQueueLen is how many chunks of received data, a packet's worth each at
// most, wait for the application to read them; more stay in the receive
// buffer.
//
// Sixteen was tried, to save the 670 bytes a connection the larger queue
// costs, and cost about 10% on the long-transfer benchmark profile: 8.11
// against 9.07 Mbps median over ten seeds each (z = -3.02), the other
// profiles unchanged. It stays at 100.
```

## Line 98 — const writeQueueLen

```
// writeQueueLen is how many writes can wait for a connection to take them.
// A Write blocks until its bytes are accepted, so more than one is waiting
// only when several goroutines write to one stream at once, and those wait
// either way. It was 100, 800 bytes allocated whole per connection.
```

## Line 141 — func newUtpStream

```
// The connection's lock is taken here and released by its event loop once
	// it has set the connection up, so a packet the socket's reader brings
	// before then waits for the setup rather than finding it half done -- and
	// nothing here waits for the loop, with the socket's dispatch lock held.
	// History: HISTORY.md, "newUtpStream: the connection's lock".
```

## Line 151 — func (*UtpStream).abandonDial

```
// abandonDial tears down a connection attempt whose caller has given up.
//
// It exists because the stream's lifetime is the socket's rather than the
// dial's (see UtpSocket.Connect): cancelling the dial context no longer takes
// the connection with it, so a dial that times out or is cancelled has to say
// so explicitly, or a half-open attempt would go on retrying its SYN with
// nobody waiting for the answer.
//
// Close is the wrong tool here: it waits for the event loop to drain, and an
// unanswered SYN keeps that loop retrying until MaxConnAttempts. Cancelling
// the stream's own context is what the old behaviour did, and it is
// immediate; the event loop's deferred cleanup removes the socket's entry for
// the connection either way.
```

## Line 167 — func (*UtpStream).abandonDial

```
// Nothing watches the context of a resting connection. A dial is never
	// resting -- its SYN is in flight -- but it costs nothing to be sure.
```

## Line 176 — func (*UtpStream).notifyRead

```
// A resting connection is woken only if the read has something to
	// release: bytes held back for want of room in the read queue, the only
	// case in which the receive window grows with a read (recvWindow counts
	// what has been handed over as read). Otherwise a receiver at rest during
	// a bulk transfer, its packets handled by the socket's reader, would have
	// its loop started for every read, to do nothing.
```

## Line 192 — func (*UtpStream).wakeLoop

```
// wakeLoop starts the connection's event loop if it is resting, for an event
// just queued for it. See dormant.go.
```

## Line 200 — func (*UtpStream).terminalErr

```
// terminalErr reports the error the connection recorded when the stream ended,
// for a reader that learned of the end from the read channel closing rather
// than from the end-of-stream marker. A clean end reports nil.
```

## Line 213 — func (*UtpStream).endedErr

```
// endedErr reports, once the connection has ended, the error it ended with,
// or nil if it is still running or ended without one. A reader that has not
// had everything the peer sent is told ErrReadClosed for an otherwise clean
// end (readEndErr); that is the reader's view, and not an error here.
```

## Line 232 — func (*UtpStream).writeErr

```
// writeErr is what a write is told once the connection has ended: the error
// it ended with, or ErrNotConnected if it ended without one.
```

## Line 241 — func (*UtpStream).endWait

```
// endWait waits, once the stream's context has ended, for its event loop to
// return, so that the caller can report why it ended rather than the bare
// context error. The loop returns promptly once its context ends; ctx bounds
// the wait regardless. It reports ctx's error if that came first.
//
// A connection whose socket was closed under it read as a clean end of
// stream, io.EOF, or as ErrReadClosed, depending on which of two ready
// channels a select took, and a write reported "context canceled". Waiting
// here is what makes each of them ErrSocketClosed, and lets a reader have
// what had already arrived first.
```

## Line 260 — func (*UtpStream).shutErr

```
// shutErr is what a write is told on a stream already shut down: the reason
// its connection ended, if it is ending, or ErrNotConnected after Close.
```

## Line 283 — func (*UtpStream).loopEnded

```
// loopEnded is the end of the connection's event loop, on whichever goroutine
// ran it last: the one start began, or one a resting connection was resumed
// on (dormant.go).
```

## Line 295 — func (*UtpStream).ReadToEOF

```
// ReadToEOF reads until the peer ends the stream, leaving everything read in
// *buf, which it reuses: what is read replaces its contents, in the room it
// already has, so a caller that knows how much is coming can size it once.
//
// It used to collect into a slice of its own, grown from nothing, and ignore
// the caller's: 4 MB read this way allocated about 20 MB.
```

## Line 321 — func (*UtpStream).ReadToEOF

```
// The connection is ending. Its loop closes the read queue as it
			// returns, behind what had arrived: read on to that.
```

## Line 346 — func (*UtpStream).Read

```
// Read reads the next available bytes from the stream into buf, returning as
// soon as anything is available. It returns io.EOF once the peer has closed
// its sending side and everything it sent has been read.
//
// This is the streaming counterpart to ReadToEOF, which only returns once the
// whole transfer is complete and so cannot be used for a connection that
// stays open. Anything that treats a uTP stream as an ordinary byte stream --
// net.Conn, io.Reader, a BitTorrent peer connection -- needs this one.
//
// Do not mix Read and ReadToEOF on the same stream. They share the same
// underlying channel and each would consume chunks the other expected.
```

## Line 369 — func (*UtpStream).Read

```
// Anything left over from the last chunk comes first.
```

## Line 387 — func (*UtpStream).Read

```
// The connection is ending. Its loop closes the read queue as it
		// returns, behind what had arrived, with the reason it ended in
		// terminalErr: what had arrived comes first, then that.
```

## Line 421 — func (*UtpStream).Write

```
// Write queues all of buf to be sent and returns once the connection has
// taken every byte of it into its send buffer, which may be before any has
// been sent: delivery continues in the background, through Close.
//
// The count is what the connection took, and what it took will be sent. If
// ctx ends first, Write returns what had been taken with ctx's error, and
// withdraws the rest, which is not sent; retrying with what was not taken
// sends each byte once. Writes from several goroutines are each sent whole,
// one after another.
//
// After CloseWrite or Close it returns ErrNotConnected, and once the
// connection has ended, the reason it ended.
```

## Line 445 — func (*UtpStream).Write

```
// The connection goroutine keeps hold of what is queued here until it has
	// copied it into the send buffer, which may be long after this call
	// returns, and the caller is then free to reuse its buffer -- net.Conn
	// makes no promise otherwise. So the bytes are copied first.
	//
	// libutp does not retain them either: utp_writev copies out of the caller's
	// iovec into packet buffers before it returns (utp_internal.cpp:1057-1066),
	// so an embedder may reuse its buffer straight away. One copy per Write is
	// what that costs, and on the fastest link the harness models it does not
	// show up at all -- see "What the write copy cost" in BENCHMARKS.md.
	// History: HISTORY.md, "UtpStream.Write: the copy".
```

## Line 468 — func (*UtpStream).lockWrite

```
// lockWrite waits for the write before this one to finish, for as long as
// ctx, the stream and its write side allow.
//
// It was a mutex, so a write waiting behind another one that the peer's
// window held up waited however long that took, its own context ended or
// not. Review of 294cc2e, finding 1.
```

## Line 486 — func (*UtpStream).lockWrite

```
// The write side may have closed while this waited.
```

## Line 500 — func (*UtpStream).lockRead

```
// lockRead waits for the reads ahead of this one, under its own context, as
// lockWrite does for writes. It was a mutex, so a read behind one blocked on
// a quiet stream waited with it for as long as that one did, whatever its own
// context said: each read's context governs that call, and the second one's
// had no say. A read that waits gives up if the read side closes, and one let
// in after it has closed is told so. Release review of 224c363, finding 2.
//
// A flag and a channel made only for a read that has to wait, rather than
// lockWrite's semaphore: every stream would carry the channel, and an idle
// connection's memory is measured against libutp's (COMPATIBILITY.md, "Memory
// per idle connection"; netem.TestIdleConnectionMemory).
```

## Line 539 — func (*UtpStream).unlockRead

```
// unlockRead ends a read, and lets the reads waiting for it try again.
```

## Line 550 — func (*UtpStream).writeChunk

```
// writeChunk is the most a write copies at once: the send buffer's size,
// which is the most the connection can take from it at once anyway.
```

## Line 559 — func (*UtpStream).writeChunked

```
// writeChunked writes total bytes, which fill copies out of the caller's
// buffers, a chunk at a time: each copied once the last has been taken. A
// write holds at most one send buffer's worth beyond what is already in the
// send buffer, however large it is.
//
// lockWrite keeps the chunks of one write together: concurrent writes are each
// written whole, one after another.
```

## Line 573 — func (*UtpStream).writeChunked

```
// One for every chunk: each is answered before the next is queued, and
	// one that is not ends the write.
```

## Line 588 — func (*UtpStream).WriteV

```
// WriteV writes several buffers as one stream write, without the caller
// having to join them first.
//
// libutp's utp_writev (utp_internal.cpp:3154-3239); utp_write is that function
// with a single iovec (:3241-3245). What it saves is a copy: Write already
// copies the caller's buffer once, so a caller holding a header and a payload
// would otherwise concatenate them (one copy) and hand the result to Write
// (a second). This makes one allocation of the exact total and copies each
// piece into it once.
//
// It returns the number of bytes written, which on success is their sum, and
// follows Write's contract rather than libutp's in one respect worth stating:
// libutp's writev is non-blocking and returns however much fitted in the
// window, leaving the rest to the caller. Write here blocks until everything
// is queued, and two different contracts for the same operation in one package
// would be worse than the difference. That deviation is Write's, not this
// one's, and is already recorded.
//
// Empty and nil buffers are skipped. A call with nothing in it writes nothing
// and returns nil, as a zero-length Write does.
```

## Line 627 — func (*UtpStream).WriteV

```
// One copy per byte -- the whole point of the call -- into one
	// allocation, or, for more than a send buffer, one per chunk.
	//
	// Chunks are filled in order, so a cursor -- which buffer, how far into
	// it -- carries on where the last chunk stopped rather than counting
	// through every buffer before it again.
```

## Line 645 — func (*UtpStream).writeQueued

```
// writeQueued hands an already-copied buffer to the connection and waits for
// it to be accepted. Shared by Write and WriteV so the two cannot drift.
//
// A connection that has ended answers nothing, so its end is waited on too:
// a write to a connection the idle timeout had closed used to wait out its
// own deadline -- 8.5 minutes in one measurement -- and then report that,
// rather than the timeout. libutp's utp_writev returns at once on a socket
// that is not connected (utp_internal.cpp:3181), its embedder having been
// told why by the error callback.
```

## Line 680 — func (*UtpStream).writeQueued

```
// What the connection has already taken will be sent, so it is
		// reported; the rest is withdrawn. The caller can retry from there
		// without sending anything twice.
```

## Line 689 — func (*UtpStream).writeQueued

```
// An answer sent before the end still counts.
```

## Line 702 — func (*UtpStream).writeQueued

```
// An answer sent before the end still counts.
```

## Line 715 — func (*UtpStream).CloseWrite

```
// CloseWrite finishes this end's sending side and leaves the reading side
// open: everything already written is flushed, a FIN goes out behind it, and
// the peer may go on sending for as long as it likes.
//
// This is the half-close, and libutp has had it all along -- `utp_shutdown(s,
// SHUT_WR)` (utp.h:176), with the socket sitting in CS_GOT_FIN while its
// application keeps reading. This library did not, which was the one place the
// reference could do something an application here could not: on reaching the
// peer's FIN the connection tore itself down, so a peer that closed its
// sending side took the whole connection with it.
//
// The name follows net.TCPConn.CloseWrite rather than libutp's shutdown(how),
// because that is what Go callers reach for and what libraries type-assert:
// anything holding a net.Conn will look for `interface{ CloseWrite() error }`.
//
// After it returns, Write reports ErrNotConnected. Read is unaffected and
// keeps returning data until the peer closes too. Close still does what it
// always did, and is what actually ends the connection.
```

## Line 740 — func (*UtpStream).CloseWrite

```
// Its own event, not the one Close uses: that one means "finished
	// entirely" and the loop sets stream.shutdown from it, which would turn
	// every half-close into a full one. This only has to wake the loop.
```

## Line 751 — func (*UtpStream).CloseRead

```
// CloseRead closes the receiving half of the stream: this end stops taking
// delivery of what the peer sends, while everything it still has to send goes
// out normally.
//
// libutp's utp_shutdown(s, SHUT_RD), which sets read_shutdown
// (utp_internal.cpp:3405-3411). What that flag does is precise and worth
// stating, because the obvious implementation is the wrong one: incoming data
// is still **acknowledged** -- `conn->ack_nr++` runs whether or not the bytes
// are delivered (:2344-2355, and again for the reorder buffer at :2392-2395)
// -- it is simply never handed to the application.
//
// So this is not "stop reading and let the window fill". A connection that let
// its receive window close would stall the peer, and a peer that cannot finish
// sending cannot finish closing. The peer keeps transmitting at full rate and
// the bytes are dropped on arrival.
//
// Data already buffered here is discarded for the same reason: it is owed to a
// reader that has said it will not come back. libutp never faces the question
// because it has no receive buffer of its own -- the bytes go straight to the
// embedder's callback or nowhere.
//
// Read reports ErrReadClosed afterwards. Write is unaffected. The name follows
// net.TCPConn.CloseRead, as CloseWrite follows its counterpart.
```

## Line 786 — func (*UtpStream).CloseRead

```
// The event only wakes the loop; the flag is what it acts on (see
	// afterPass), so an event dropped from a full queue loses nothing.
```

## Line 796 — func (*UtpStream).readClosedSignal

```
// readClosedSignal is closed once CloseRead has been called.
//
// A Read waiting on an open, silent stream used to wait through CloseRead:
// the flag was checked only before the wait, as net.TCPConn's CloseRead does
// not leave a blocked Read waiting. From the release review of 9c44c4f (R6).
```

## Line 813 — func (*UtpStream).Done

```
// Done is closed once the connection has ended and holds nothing more: its
// close handshake finished or gave up, it was reset or timed out, or the
// socket closed under it. libutp's UTP_STATE_DESTROYING. Data it delivered
// before then can still be read.
```

## Line 821 — func (*UtpStream).SetWindowSize

```
// SetWindowSize sets the ceiling on this connection's congestion window, in
// bytes: ConnectionConfig.WindowSize for one connection, after it has been
// made. It is libutp's utp_setsockopt(UTP_SNDBUF) -- libutp's "send buffer"
// is this ceiling, not a buffer (utp_internal.cpp:936, :1710) -- and takes
// effect at once: nothing more is sent past it, and a window above it is cut
// to it. Zero restores the default.
```

## Line 831 — func (*UtpStream).WindowSize

```
// WindowSize reports the ceiling SetWindowSize or the connection's config
// set: libutp's utp_getsockopt(UTP_SNDBUF).
```

## Line 837 — func (*UtpStream).Close

```
// Close ends the stream and returns at once. What has been written goes on
// being delivered in the background, the FIN after it, until the peer has
// acknowledged everything or stopped answering; closing the socket waits for
// that (UtpSocket.Close). libutp's utp_close (utp_internal.cpp:3358-3380).
```

## Line 847 — func (*UtpStream).Close

```
// Tell the connection's final drain not to wait for a reader that is
		// no longer there: the loop would otherwise block handing over bytes
		// that this consumer has just said it does not want.
```

## Line 851 — func (*UtpStream).Close

```
// Wake the event loop.
		//
		// Setting the flag alone is not enough: the loop is blocked in a
		// select, and nothing here is one of the cases it waits on. It would
		// only notice the shutdown when some unrelated packet or timer
		// happened to arrive, so Close took anywhere from about a second to
		// the full idle timeout depending on what else was in flight. The
		// shutdown event is the channel the socket already uses to tell a
		// connection to wind up.
```

## Line 863 — func (*UtpStream).Close

```
// The queue is full, so the loop has plenty to wake it and will
			// see the flag on its next pass.
```

## Line 867 — func (*UtpStream).Close

```
// Return, and let the connection finish in the background: libutp's
		// utp_close queues the FIN, sets close_requested and returns
		// (utp_internal.cpp:3358-3380), and the socket goes on sending until
		// utp_check_timeouts retires it. Closing the socket waits for it
		// (UtpSocket.awaitLingering), which is where an exiting program would
		// otherwise lose whatever was still in flight. History: HISTORY.md,
		// "UtpStream.Close: returning at once".
```

## Line 885 — const closeStallTimeout

```
// closeStallTimeout is how long Close keeps waiting for a connection that has
// heard nothing from its peer.
//
// Two seconds is twice libutp's RTO floor (`rto = max(rtt + rtt_var * 4,
// 1000)`, utp_internal.cpp:1380), so a single lost FIN and its first
// retransmission do not read as a stall. Past that the peer has missed two
// chances to answer and waiting longer buys the caller nothing: the connection
// goes on retransmitting in the background either way, and reaches the same
// end whether or not anyone is watching.
```

## Line 896 — const closeStallPollInterval

```
// closeStallPollInterval is how often that is checked. It only bounds how
// quickly a finished close is noticed, so it is small enough not to add
// latency to the common case -- where the connection ends in one round trip
// and the wait is over before the first tick.
```

## Line 902 — func (*UtpStream).waitForFlush

```
// waitForFlush waits for the connection's event loop to finish, giving up once
// the peer has gone quiet for closeStallTimeout. It reports whether the loop
// actually finished.
//
// Giving up means giving up on *waiting*, not on the connection: the caller
// stops being held, and the event loop keeps running, keeps retransmitting and
// exits on its own. Nothing queued is discarded by returning early -- which is
// why the caller must not cancel the stream context on this path.
```

## Line 942 — func (*UtpStream).peerActivity

```
// peerActivity reports how many packets the connection has received, or zero
// if there is no connection to ask.
```
