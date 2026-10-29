# socket_accept.go

Every comment in `socket_accept.go` at e56c9aa, verbatim.

## Line 20 — type acceptRequest

```
// mu orders the handover against the caller giving up: a connection
	// is either queued for a caller still waiting, or closed. See hand.
```

## Line 24 — type acceptRequest

```
// onHand, when set, takes the result in place of a caller: the socket's
	// own request for a SYN it answered with nobody waiting (holdAnswered).
```

## Line 39 — func (*acceptRequest).hand

```
// hand gives the caller its result, or, if the caller has already given up,
// closes the connection the result carries.
//
// An accepted connection runs on the socket's context, not the Accept call's
// (see selectAcceptHelper), so one handed to a caller that has gone would run
// for nobody -- with no idle timeout by default, for as long as the peer
// stayed. libutp hands each incoming connection to its application from
// inside the call that received the SYN (utp_call_on_accept,
// utp_internal.cpp:3001), so it has no such gap to close.
```

## Line 68 — func (*acceptRequest).giveUp

```
// giveUp records that the caller has stopped waiting, and returns a result
// already handed over, if there is one: the caller takes it rather than
// leaving it behind.
```

## Line 83 — func (*acceptRequest).wait

```
// wait is what Accept and AcceptWithCID do once the request is queued.
//
// It ends with the socket as well as with the caller. It used to watch only
// the caller's context, so an Accept on context.Background() outlived the
// socket it was queued on: the dispatcher that would have answered it was
// gone, and so, for AcceptWithCID, was the expiry wheel that would have timed
// it out. From the release review of 9c44c4f (R7).
```

## Line 123 — func (*Socket).eventLoop

```
// Receive without acting, so that a virtual clock can be told the
		// loop is running again before any case body can create a
		// connection. See idleBarrier.
```

## Line 134 — func (*Socket).eventLoop

```
// Every connection's context ends with this one, and a resting
			// connection has no loop to see it.
```

## Line 142 — func (*Socket).eventLoop

```
// An Accept changes the routing tables the reader consults, so it is
		// served under the same lock. Incoming packets no longer pass through
		// this loop: the reader routes them itself (dispatch).
```

## Line 155 — func (*Socket).holdAnswered

```
// holdAnswered keeps a connection the socket answered with nobody waiting,
// until an Accept takes it (takeAnswered). Called with dispatchMu held, as
// the SYN is answered: no Accept is waiting then, or it would have had the
// connection, and any that comes later looks here first.
```

## Line 160 — func (*Socket).holdAnswered

```
// Those that ended while waiting -- reset, timed out, closed by the
	// peer -- go now: they are no longer counted as connections, and kept
	// here they would pile up behind a socket nobody accepts on.
```

## Line 175 — func (*Socket).takeAnswered

```
// takeAnswered removes and returns the oldest answered connection still
// alive, or the one for key if key is not empty; nil if there is none.
// Called with dispatchMu held.
```

## Line 223 — func (*Socket).handleNewAcceptEvent

```
// A connection already answered satisfies this immediately, and so does
	// a SYN that already arrived.
```

## Line 233 — func (*Socket).handleNewAcceptEvent

```
// Otherwise wait for one, so that the ordinary server pattern -- call
	// Accept, then have a client connect -- works: the SYN that arrives a
	// moment later finds the request.
	//
	// Those already abandoned go first: pruned on each arrival, the list
	// holds at most the live requests and those abandoned since the last
	// one. History: HISTORY.md, "handleNewAcceptEvent: waiting".
```

## Line 250 — func (*acceptRequest).abandoned

```
// abandoned reports whether the caller has stopped waiting for this request.
```

## Line 260 — func (*Socket).takePendingAccept

```
// takePendingAccept returns the oldest waiting cid-less Accept whose caller
// has not already given up, or nil.
```

## Line 266 — func (*Socket).takePendingAccept

```
// Skip callers that have already returned.
```

## Line 318 — func (*Socket).AcceptBacklog

```
// AcceptBacklog reports how many incoming connections are waiting for an
// Accept to take them.
```

## Line 358 — func (*Socket).selectAcceptHelper

```
// selectAcceptHelper makes the connection for an incoming SYN and hands it to
// accept, once connected.
//
// The connection runs on the socket's context, whichever came first, the SYN
// or the Accept: an accepted connection outlives the Accept that produced it,
// as a dialled one outlives its dial. Where the SYN came first this used the
// Accept call's context, and a timeout on Accept that fired after it had
// returned ended the connection it had returned.
```
