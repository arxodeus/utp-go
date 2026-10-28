# compat/golibutp/socket.go

Every comment in `compat/golibutp/socket.go` at e56c9aa, verbatim.

## Line 21 — var mu, sockets, errSocketClosed

```
// mu guards every Socket's and Conn's mutable fields, as go-libutp's
	// package lock does. Nothing that can call back into this package --
	// the engine's firewall and write-error hooks -- is called with it held.
```

## Line 25 — var mu, sockets, errSocketClosed

```
// sockets is every open Socket, for WriteStatus.
```

## Line 31 — type FirewallCallback

```
// A FirewallCallback returns true if an incoming connection request from the
// address should be refused. A refused SYN gets no answer, which is better
// than accepting and closing: the peer learns nothing.
```

## Line 36 — type NewSocketOpt

```
// NewSocketOpt configures a Socket at NewSocket.
```

## Line 39 — func WithLogger

```
// WithLogger sets the Socket's logger, in place of Logger.
```

## Line 46 — type Socket

```
// Socket is a uTP endpoint on a packet connection. It is a net.PacketConn
// for the datagrams that are not uTP, which is how a BitTorrent client runs
// its DHT on the same port, and a net.Listener for incoming connections.
```

## Line 54 — type Socket

```
// minLogLevel is the least slog level passed to logger; see logHandler.
```

## Line 57 — type Socket

```
// Guarded by mu.
```

## Line 61 — type Socket

```
// dialing is the connection attempts under way, by the address they
	// are for, each with the means to fail it.
```

## Line 94 — func NewSocket

```
// NewSocket listens on network ("udp", "udp4", "udp6", or "inproc" for
// missinggo's in-process network) at addr.
```

## Line 104 — func NewSocketFromPacketConn

```
// NewSocketFromPacketConn makes a Socket on a packet connection the caller
// already has. The Socket takes ownership of it.
```

## Line 135 — func (*Socket).firewall

```
// firewall is the engine's firewall hook: the synchronous callback if there
// is one, as go-libutp prefers it, and otherwise the asynchronous one. Both
// are asked only about a connection request, and neither with mu held.
```

## Line 148 — func (*Socket).onWriteError

```
// onWriteError is go-libutp's handling of a failed send (its sendtoCallback):
// a connection's OnError handler hears of it; with none, an error that says
// the address can never be reached fails the connection, and anything else
// is left to retransmission.
```

## Line 182 — func (*Socket).onWriteError

```
// Not here: this runs on whatever goroutine sent the datagram,
		// which may be the connection's own, and closing it waits on that.
```

## Line 194 — func (*Socket).Close

```
// Close closes the socket and every connection on it, at once.
```

## Line 211 — func (*Socket).Addr

```
// Addr is the local address, as net.Listener has it.
```

## Line 216 — func (*Socket).LocalAddr

```
// LocalAddr is the local address, as net.PacketConn has it.
```

## Line 221 — func (*Socket).Accept

```
// Accept waits for the next incoming connection.
//
// A dial completes whether or not anything is waiting here: the engine
// answers a SYN as it arrives and holds the connection until an Accept takes
// it, as libutp hands it to go-libutp's backlog. go-libutp closes any beyond
// five waiting; these are held, counted against the socket's connection cap
// (libutp's 3000), until taken.
```

## Line 242 — func (*Socket).Dial

```
// Dial connects to addr and waits as long as libutp does for an answer.
```

## Line 247 — func (*Socket).DialTimeout

```
// DialTimeout connects to addr, waiting at most timeout; zero is no limit
// beyond libutp's own.
```

## Line 259 — func (*Socket).DialContext

```
// DialContext connects to addr. An empty network is the Socket's own.
```

## Line 297 — func (*Socket).DialContext

```
// a send that can never succeed
```

## Line 310 — func (*Socket).ReadFrom

```
// ReadFrom returns the next datagram that was not uTP.
```

## Line 315 — func (*Socket).WriteTo

```
// WriteTo sends a datagram as it is.
```

## Line 320 — func (*Socket).SetDeadline

```
// SetDeadline sets ReadFrom's and WriteTo's deadlines. go-libutp panics
// here; connections carry their own deadlines either way.
```

## Line 326 — func (*Socket).SetReadDeadline

```
// SetReadDeadline bounds how long ReadFrom waits.
```

## Line 331 — func (*Socket).SetWriteDeadline

```
// SetWriteDeadline bounds WriteTo.
```

## Line 336 — func (*Socket).ReadBufferLen

```
// ReadBufferLen is the RecvBuffer option.
```

## Line 343 — func (*Socket).WriteBufferLen

```
// WriteBufferLen is the SendBuffer option: libutp's opt_sndbuf, the
// ceiling on each connection's congestion window.
```

## Line 351 — func (*Socket).SetWriteBufferLen

```
// SetWriteBufferLen sets the SendBuffer option for connections made from now
// on, and panics on a value libutp refuses, as go-libutp does.
```

## Line 359 — func (*Socket).SetOption

```
// SetOption sets a libutp context option, which connections made from then
// on take: utp_context_set_option. It returns 0, or -1 for an option or
// value libutp does not take.
```

## Line 374 — func (*Socket).SetFirewallCallback

```
// SetFirewallCallback sets a firewall callback. go-libutp calls this one for
// every datagram, without its lock, before knowing whether the datagram is a
// connection request; here it is asked only about connection requests,
// which is all go-libutp uses its answer for. A synchronous callback, if
// set, is preferred to it.
```

## Line 385 — func (*Socket).SetSyncFirewallCallback

```
// SetSyncFirewallCallback sets the firewall callback, asked about each
// incoming connection request before anything is created for it. Unlike
// go-libutp's, it is not called with this package's lock held, so it may
// take locks that code using this package also holds. It must not block.
```

## Line 395 — func WriteStatus

```
// WriteStatus writes a summary of every open Socket.
```
