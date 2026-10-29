# connection_lifetime_test.go

Every comment in `connection_lifetime_test.go` at e56c9aa, verbatim.

## Line 11 — func loopbackSockets

```
// loopbackSockets binds two sockets on the loopback address.
```

## Line 27 — func TestAcceptedConnectionOutlivesItsAccept

```
// An accepted connection belongs to the socket, not to the Accept call that
// produced it, as a dialled one outlives its dial.
//
// Where the SYN came first and waited for an Accept, the connection was made
// on the Accept call's context, so it ended the moment that context did --
// a timeout on Accept that fired after it had returned killed the
// connection it had returned. Where the Accept came first, it was made on
// the socket's, so which of the two a server got depended on timing.
```

## Line 60 — func TestAcceptedConnectionOutlivesItsAccept

```
// The SYN waits at the server until an Accept claims it.
```

## Line 99 — func TestSocketCloseIsReportedToTheConnection

```
// A connection that ends because its socket was closed says so: the reader
// gets what had already arrived, then ErrSocketClosed, and so does a writer.
//
// It read as a clean end of stream half the time -- io.EOF, which tells the
// application the peer finished -- and as ErrReadClosed, "read side closed",
// the other half, depending on which of two channels a select saw first.
// libutp tells its application the socket is being destroyed
// (UTP_STATE_DESTROYING, utp_internal.cpp:2490), not that the stream ended.
```

## Line 134 — func TestSocketCloseIsReportedToTheConnection

```
// Arrived and waiting for the reader before the socket goes.
```

## Line 160 — func TestConnectionForAnAbandonedAcceptIsClosed

```
// A connection handed to an Accept whose caller has already given up is
// closed, not left running for nobody. Accepted connections run on the
// socket's context, and with no idle timeout by default one that nobody
// holds would never end.
```

## Line 187 — func TestConnectionForAnAbandonedAcceptIsClosed

```
// The handover racing the caller's context: the caller gives up first.
```
