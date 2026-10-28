# zerowindow_test.go

Every comment in `zerowindow_test.go` at e56c9aa, verbatim.

## Line 9 — func TestZeroWindowProbe

```
// A peer that advertises a zero receive window stops this sender. Something
// has to start it again.
//
// The peer will send a window update when its application drains its buffer,
// but that update is a single packet on an unreliable path. If it is lost,
// the sender has nothing outstanding -- so no retransmission timer -- and no
// reason of its own to transmit. The connection stops dead until the idle
// timeout kills it, with data still queued and a peer waiting for it.
//
// libutp answers this with a probe: when an acknowledgement reports a zero
// window it arms a timer, and when that expires it forces the peer's window
// up to one packet (utp_internal.cpp:2145-2151 and :1142-1145). One packet
// goes out, the peer acknowledges it, and the acknowledgement carries a fresh
// window. Recovery costs one packet per interval and needs nothing from the
// peer beyond an ack.
```

## Line 26 — func TestZeroWindowProbe

```
// libutp's interval is 15 seconds. Testing that honestly would mean a
	// 15-second test, so the interval is configurable -- as InitialTimeout,
	// MinTimeout and MaxTimeout already are -- and defaults to libutp's.
```

## Line 53 — func TestZeroWindowProbe

```
// Answer the SYN, advertising a full window so the connection completes.
```

## Line 64 — func TestZeroWindowProbe

```
// Now tell it the window is closed.
```

## Line 70 — func TestZeroWindowProbe

```
// Queue data. Nothing may go out while the window is closed.
```

## Line 83 — func TestZeroWindowProbe

```
// Once the probe interval has passed, exactly one packet must go out.
```

## Line 108 — func TestKeepAlive

```
// An established connection that goes quiet must say something occasionally.
//
// libutp sends a keep-alive after 29 seconds of silence
// (utp_internal.cpp:74, :1271-1274), chosen to sit under the 30-second UDP
// mapping timeout common in NATs. A connection quiet for longer than that
// loses its mapping and cannot be reached from outside again -- and a peer
// with an idle timeout shorter than ours eventually tears it down while we
// still believe it is alive.
//
// This library sent nothing at all. It relied entirely on the peer speaking
// first, which against another copy of this library means neither side ever
// does.
```

## Line 122 — func TestKeepAlive

```
// libutp's interval is 29 seconds; configurable so this is not a
	// half-minute test.
```

## Line 158 — func TestKeepAlive

```
// Now say nothing to it, and wait.
```

## Line 170 — func TestKeepAlive

```
// libutp acks one behind, so the packet reads as a stale
			// acknowledgement and the peer answers it.
```
