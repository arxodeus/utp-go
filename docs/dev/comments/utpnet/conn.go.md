# utpnet/conn.go

Every comment in `utpnet/conn.go` at e56c9aa, verbatim.

## Line 13 — var ErrNilPeer

```
// ErrNilPeer is returned when a write names no destination.
```

## Line 16 — type Conn

```
// Conn is a uTP stream presented as a net.Conn.
```

## Line 34 — func newConn

```
// Kept for as long as the uTP socket has the connection, from
			// before this was made (demuxConn.HoldPeer).
```

## Line 48 — func (*Conn).Stream

```
// Stream returns the underlying uTP stream, for callers that need the
// context-taking API or the connection id.
```

## Line 58 — func (*Conn).Write

```
// Write writes all of b, or reports how much of it was written.
//
// When the deadline moves while it waits, it carries on with what is left:
// the stream reports what it had already taken (see Stream.Write), and
// those bytes will be sent. It used to start over from the beginning on any
// write that came back with nothing reported -- and the stream reported
// nothing even when it had taken bytes -- so a deadline moved during a
// blocked write sent its bytes twice. From the release review of 9c44c4f
// (R1).
```

## Line 73 — func (*Conn).writeContinuing

```
// writeContinuing runs a write under the write deadline, carrying on from
// what has been written when the deadline moves: op writes what is left from
// byte from, and reports how much of it the stream took.
```

## Line 82 — func (*Conn).writeContinuing

```
// Nothing reported, so that withDeadline goes on with the rest
			// if the deadline moved.
```

## Line 91 — func (*Conn).withDeadline

```
// withDeadline runs op under a context derived from d, and translates what
// comes back into what a net.Conn caller expects.
//
// The loop is there because a deadline can move while op is blocked, which
// the standard library supports and callers use -- one goroutine blocked in
// Read, another calling SetReadDeadline to unblock it. Moving the deadline
// cancels op's context; this then re-reads the deadline and either retries
// against the new one or reports the timeout. Without the loop, moving a
// deadline surfaced as "context canceled", which no net.Conn caller checks
// for and every one of them would treat as fatal.
```

## Line 114 — func (*Conn).withDeadline

```
// The deadline moved rather than the socket closing. Try
				// again against the deadline as it now stands; if that has
				// already passed, the next pass returns a timeout.
```

## Line 127 — func translateDeadlineError

```
// translateDeadlineError maps a context expiry onto the net.Error a caller
// expects, and leaves everything else alone.
//
// Without this, a Read that timed out would return "context deadline
// exceeded", which no net.Conn caller checks for -- they check
// err.(net.Error).Timeout() -- and would be treated as a fatal error that
// tears the connection down.
```

## Line 169 — type deadline

```
// deadline is a net-style deadline: settable at any time, from any goroutine,
// affecting calls already in progress.
//
// The standard library's deadlines have that property and callers rely on it
// -- a common pattern is one goroutine blocked in Read and another calling
// SetReadDeadline(time.Now()) to unblock it. A context captured when Read
// started cannot do that, so the deadline is kept here and a fresh context is
// derived per call.
```

## Line 180 — type deadline

```
// changed is closed and replaced whenever the deadline moves, so a
	// blocked waiter can notice.
```

## Line 197 — func (*deadline).context

```
// context derives a context that expires at the deadline, and returns the
// channel that closes if the deadline is moved.
//
// It reports true if the deadline has already passed, in which case the
// caller should not attempt the operation at all.
```

## Line 223 — func (*deadline).context

```
// The deadline moved while the operation was in flight. Cancel so
			// the caller re-evaluates against the new one.
```

## Line 232 — func (*deadline).snapshot

```
// expired reports whether the deadline has already passed.
//
// It exists for callers that want only that answer. wait starts a goroutine to
// serve its channel; a caller that discards the channel would leak it, which
// is what WriteTo used to do -- once per datagram, for the life of the
// process. See TestSharedPortDoesNotLeakGoroutines.
// snapshot is the deadline as it stands, and the channel that closes when it
// moves.
```

## Line 253 — func (*deadline).wait

```
// wait returns a channel that fires at the deadline, reports whether the
// deadline has already passed, and returns a stop function the caller must
// call when it is done with the channel. A zero deadline gives a channel that
// never fires until the deadline is changed.
//
// The stop function is the whole point of the signature. Serving the channel
// takes a goroutine, and until this returned one there was no way to end that
// goroutine early: it parked until the deadline was next *changed*, which for
// a caller that never sets one is never. Every ReadFrom left one behind.
//
// Call stop exactly once, on every path out.
```

## Line 304 — func (*Conn).CloseWrite

```
// CloseWrite finishes the sending side and leaves the reading side open, as
// net.TCPConn.CloseWrite does.
//
// It is here because callers type-assert for it: anything holding a net.Conn
// that wants a half-close looks for `interface{ CloseWrite() error }` rather
// than for a concrete type. A BitTorrent client uses it to say it has finished
// uploading while it is still downloading.
```

## Line 315 — func (*Conn).WriteBuffers

```
// WriteBuffers writes several buffers as one stream write, without joining
// them first. It mirrors net.Buffers' shape, and libutp's utp_writev.
//
// The saving is one copy. Writing a header and a payload separately costs two
// Write calls (and two packets' worth of framing decisions); joining them
// first costs a copy into the joined slice and another inside Write. This
// costs one.
//
// net.Buffers.WriteTo cannot reach this on its own -- the standard library
// dispatches to an unexported interface only *net.TCPConn satisfies -- so a
// caller that wants the saving calls it directly.
//
// A deadline moved while it waits is carried on from, as Write does: it went
// straight to withDeadline, which retries only a write that took nothing, so
// one that had taken part of the buffers returned "context canceled" on an
// ordinary deadline extension. Review of 294cc2e, finding 3.
```

## Line 338 — func buffersFrom

```
// buffersFrom is bufs without its first n bytes, sharing their storage.
```

## Line 350 — func (*Conn).CloseRead

```
// CloseRead finishes the receiving side and leaves the sending side open, as
// net.TCPConn.CloseRead does. What the peer sends afterwards is still
// acknowledged -- so the peer is never stalled and can finish its own close --
// and then discarded.
//
// Type-asserted for in the same way as CloseWrite:
// `interface{ CloseRead() error }`.
```
