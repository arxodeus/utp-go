# socket_dial.go

Every comment in `socket_dial.go` at e56c9aa, verbatim.

## Line 9 — const cidRandomDraws

```
// cidRandomDraws is how many random receive ids generateCid tries before it
// looks at every one in turn.
```

## Line 13 — var errNoFreeConnectionId

```
// errNoFreeConnectionId is a dial to a peer that already uses, or used in the
// last idQuarantine, every pair of ids a new connection could take.
```

## Line 17 — func (*Socket).generateCid

```
// generateCid picks ids for a new connection to peer, and registers eventCh
// under them if it is given.
//
// Random draws first, as libutp draws (utp_internal.cpp:2533-2538), and then
// every receive id in turn from a random one: libutp's loop, and this one,
// had no end, and a socket whose peer held or had recently held enough of its
// ids would have spun for ever inside Connect, which holds dispatchMu: the
// lock the socket's reader takes for every datagram, so every connection on
// the socket stalled with it. A connection takes two ids and keeps them from
// its neighbours,
// so 28,365 connections to one peer, live or quarantined, used every id
// (TestDialWithNoFreeIdFails).
```

## Line 44 — func (*Socket).generateCid

```
// wrapping
```

## Line 47 — func (*Socket).generateCid

```
// Calculate send ID based on initiator status
```

## Line 58 — func (*Socket).generateCid

```
// Free means no connection to this peer receives on the id yet,
		// whichever side opened it: packets are routed on the receive id
		// alone (routeCandidates). libutp draws until the (address, receive
		// id) key is free (utp_internal.cpp:2533-2538). This checked only the
		// exact pair, so a new connection could take the receive id an
		// accepted one already had.
		//
		// Nor one that ended in the last idQuarantine: what is still in
		// flight for it, or still coming from a peer finishing its side of
		// it, would reach the new connection. A RESET for the old one
		// arriving while the new one dialled refused it -- the long soak
		// drew that once a minute or so, the same ids picked 0.18s after
		// the last connection to hold them closed. TCP keeps a closed
		// connection's ports out of use for the same reason (TIME_WAIT);
		// libutp does not. See DEVIATIONS.md, "A closed connection's ids
		// are not reused at once".
		//
		// And neither of its ids may be one another connection to the peer
		// uses in either direction. A RESET is matched on the receive id
		// first, as libutp matches it, so a connection sending on the id
		// another receives on has its RESETs taken by the other: in the soak
		// a dialling connection was refused by a RESET meant for one that
		// sent on its receive id and had just closed. libutp draws only the
		// receive id free and has the same ambiguity. With every id used by
		// one connection at most, no packet can be for two.
```

## Line 98 — func (*Socket).Connect

```
// Create channels for connection status and events
```

## Line 102 — func (*Socket).Connect

```
// Registered under the reader's lock, so the reader never finds the
	// connection's queue without the connection behind it. Nothing here
	// waits: a packet for the connection that arrives before its event loop
	// has set it up waits on the connection's own lock. See newUtpStream.
```

## Line 107 — func (*Socket).Connect

```
// Generate connection ID
```

## Line 114 — func (*Socket).Connect

```
// Create new UTP stream
```

## Line 116 — func (*Socket).Connect

```
// The connection's lifetime is the socket's, not the dial's.
	//
	// This passed `ctx` -- the caller's dial context -- and so gave every
	// connection a lifetime that ended when the dial did. net.Dialer
	// documents the opposite ("Canceling ctx does not affect the connection
	// after it is established") and Go callers rely on it: the standard shape
	// is a dial under a timeout with `defer cancel()`, which fires the moment
	// the dial returns. anacrolix/torrent does exactly that, and the result
	// was a connection that completed its BitTorrent handshake and then died
	// mid-stream with "context canceled" -- a torrent that never moved a
	// byte. See TestDialContextCancelDoesNotCloseTheConnection.
	//
	// The caller's context still governs the wait below, which is the part it
	// is for.
```

## Line 146 — func (*Socket).Connect

```
// Wait for connection result
```

## Line 154 — func (*Socket).Connect

```
// A closed channel with nothing in it means the connection went away
		// without ever reporting an outcome. Saying "timed out" here was
		// wrong twice over: it was not a timeout, and until the connection
		// started reporting success by sending rather than by closing, this
		// was the branch every successful connection took.
```

## Line 161 — func (*Socket).Connect

```
// The attempt is the caller's to abandon even though the stream no
		// longer shares its context.
```

## Line 180 — func (*Socket).ConnectWithCID

```
// See the note in Connect.
```

## Line 192 — func (*Socket).ConnectWithCID

```
// The socket's context, not the dial's -- see the note in Connect.
```

## Line 208 — func (*Socket).ConnectWithCID

```
// Honour the caller's context, as Connect does. A bare receive here meant
	// that cancelling the context during connection setup never returned: the
	// connection's event loop exits on the same cancellation without
	// signalling this channel, so the caller waited forever.
```

## Line 218 — func (*Socket).ConnectWithCID

```
// Report the reason rather than assuming one. This used to return a
		// flat "connection timed out" whatever had happened, which was true
		// while running out of SYN attempts was the only way to fail; an
		// ICMP error is a second way, and "refused" and "timed out" are
		// exactly the distinction libutp draws with UTP_ECONNREFUSED against
		// UTP_ECONNRESET (utp_internal.cpp:3122). Wrapped, so errors.Is finds
		// ErrConnRefused and ErrTimedOut.
```

## Line 240 — func (*Socket).awaitConnected

```
// Report against the stream's connection id, not the accept's. A cid-less
	// Accept has no cid of its own, and reading accept.cid here crashed the
	// moment anyone used that path.
```
