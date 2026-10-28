# compat/golibutp/conn.go

Every comment in `compat/golibutp/conn.go` at e56c9aa, verbatim.

## Line 14 — var ErrConnClosed, errConnDestroyed

```
// ErrConnClosed is returned by a Conn's Read and Write after its Close.
```

## Line 19 — type Conn

```
// Conn is a uTP connection.
```

## Line 26 — type Conn

```
// remoteKey is remoteAddr as the engine's write-error hook names it.
```

## Line 29 — type Conn

```
// Guarded by mu.
	//
	// us is the engine's stream while the connection lives, and nil once
	// it has ended or its Socket has closed: go-libutp's field of that name,
	// which is nil once libutp has destroyed the socket.
```

## Line 42 — var testHookConnPublished

```
// testHookConnPublished, when set, runs in newConn just after the connection
// is published to its socket: the moment a concurrent Socket.Close can reach
// it.
```

## Line 58 — func (*Socket).newConn

```
// Taken from the stream before c is published: once it is in s.conns,
	// c.us is mu's, and Socket.Close sets it to nil.
```

## Line 82 — func (*Conn).fail

```
// fail ends the connection with err, which its Read and Write then return.
```

## Line 92 — func (*Conn).translate

```
// translate turns what the engine returned into what go-libutp returns:
// a failure's own error, ErrConnClosed after Close, and otherwise the
// engine's -- io.EOF at the end of the stream, a net.Error on a deadline.
```

## Line 135 — func (*Conn).Close

```
// Close closes the connection. What has been written is still delivered;
// Read and Write return ErrConnClosed from now on.
```

## Line 151 — func (*Conn).WriteBufferLen

```
// WriteBufferLen is this connection's SendBuffer: libutp's opt_sndbuf, the
// ceiling on its congestion window (utp_getsockopt).
```

## Line 157 — func (*Conn).SetWriteBufferLen

```
// SetWriteBufferLen sets this connection's SendBuffer, with effect at once
// (utp_setsockopt). It panics on a value libutp refuses, as go-libutp does.
```

## Line 166 — func (*Conn).OnError

```
// OnError sets a handler for errors that do not end the connection, such as
// a datagram the operating system would not send. It is called on a
// goroutine of its own.
```
