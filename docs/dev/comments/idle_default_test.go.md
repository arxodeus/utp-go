# idle_default_test.go

Every comment in `idle_default_test.go` at e56c9aa, verbatim.

## Line 9 — func TestSilentPeerDoesNotCloseAnIdleConnectionByDefault

```
// By default a connection is never closed for silence, as in libutp.
//
// libutp has no idle timeout: a connection with nothing in flight ends only by
// its application, a reset, or the retransmission timeout giving up, and that
// counts only while something is outstanding (utp_internal.cpp:1239-1240).
// Measured against its driver, a connection whose peer went quiet was still
// open after 600 seconds, having sent 20 keep-alives. This library closed one
// after 60, which ended a connection that an outage of a minute or more had
// interrupted and that libutp would have resumed. Here the peer is silent for
// ten minutes and then speaks: the connection must have kept sending its
// keep-alives, and must take the data.
```

## Line 64 — func TestSilentPeerDoesNotCloseAnIdleConnectionByDefault

```
// The acknowledgement of the SYN was the last packet; from here the peer
	// says nothing, and the connection keeps its keep-alives going.
```

## Line 88 — func TestSilentPeerDoesNotCloseAnIdleConnectionByDefault

```
// The peer comes back.
```
