# native/libutp/bridge.h

Every comment in `native/libutp/bridge.h` at e56c9aa, verbatim.

## Line 1

```
// bridge.h -- a small C API over libutp, for interoperability testing.
//
// libutp is an embedding library: it does no I/O of its own and is not
// thread-safe. This bridge gives it a UDP socket and an event loop, serialises
// every call into that loop, and exposes a handful of blocking-free entry
// points that Go can drive.
//
// One peer owns one connection. That is all an interop test needs, and it
// keeps the lifetime rules -- particularly that a utp_socket must not be
// touched after UTP_STATE_DESTROYING -- simple enough to get right.
```

## Line 26

```
// Create a peer bound to 127.0.0.1 on the given port. Pass 0 to let the
// kernel choose; libutp_peer_port then reports what was chosen.
// Returns NULL on failure.
```

## Line 31

```
// Destroy a peer. The event loop must have returned first.
```

## Line 34

```
// Run the event loop until libutp_peer_stop. Blocks; call it on its own
// goroutine.
```

## Line 41

```
// Start an outgoing connection to 127.0.0.1:remote_port. Returns 0 if the
// attempt was started, -1 if a socket already exists.
```

## Line 45

```
// Accept the next incoming connection. Until this is called, incoming SYNs
// are refused by the firewall callback.
```

## Line 52

```
// Queue data for sending. Returns the number of bytes queued, which is always
// len: the queue grows as needed and libutp drains it as the window allows.
```

## Line 56

```
// Bytes still queued and not yet handed to libutp.
```

## Line 59

```
// Copy out received data. Returns the number of bytes copied, 0 if none is
// buffered yet.
```

## Line 63

```
// Close once everything queued has been handed to libutp.
```

## Line 70

```
// LIBUTP_BRIDGE_H
```
