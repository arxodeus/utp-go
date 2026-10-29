# native/libutp/bridge.cpp

Every comment in `native/libutp/bridge.cpp` at e56c9aa, verbatim.

## Line 1

```
// bridge.cpp -- see bridge.h.
//
// Every libutp entry point is called with peer->mu held, from either the event
// loop or a Go-facing function. libutp is not thread-safe, so that lock is the
// whole concurrency story.
```

## Line 24

```
// --- a growable byte buffer -------------------------------------------------
// Plain malloc rather than std::vector: libutp is built -fno-exceptions, and
// there is no reason to drag the C++ allocator in for two byte queues.
```

## Line 28

```
// Every section below that touches libutp holds both the peer's own lock and
// the process-wide one, always in that order. See global_lock.h: separate
// utp_contexts on separate threads still share utp_writev's static iovec.
```

## Line 42

```
// byte_buf holds len bytes at data + head. Consuming moves head on; the bytes
// are moved to the front only when an append needs the room, so taking a
// packet's worth off a 16 MB send queue does not move the other 16 MB.
```

## Line 95

```
// --- peer -------------------------------------------------------------------
```

## Line 101

```
// UDP socket
```

## Line 102

```
// self-pipe, to break select() promptly
```

## Line 134

```
// pump_writes hands as much queued data to libutp as its window allows.
// Must be called with mu held.
```

## Line 139

```
// window full; UTP_STATE_WRITABLE will call back
```

## Line 143

```
		// Everything queued is now libutp's problem; it will flush before it
		// destroys the socket.
```

## Line 150

```
// --- libutp callbacks -------------------------------------------------------
```

## Line 165

```
	// Tell libutp the data is out of its buffer, so its receive window reopens.
```

## Line 184

```
		// The socket pointer is invalid from here on.
```

## Line 201

```
	// Non-zero refuses the connection.
```

## Line 215

```
// The path MTU, which libutp uses as the ceiling of its own MTU search
// (mtu_reset, utp_internal.cpp:1314-1322). Without this callback
// utp_call_get_udp_mtu returns 0 (utp_callbacks.cpp:122), leaving the ceiling
// at 0 against a floor of 576 and settling the search on 288-byte packets --
// so libutp moves the same bytes in five times as many datagrams. See the
// matching note in driver.cpp.
//
// 1472 is a standard 1500-byte Ethernet MTU less 20 bytes of IPv4 header and 8
// of UDP. The peer runs over loopback, whose MTU is larger, but reporting the
// ordinary path MTU is what an embedder on an ordinary path does.
```

## Line 232

```
	// How much libutp has handed us that the application has not read yet.
	// This is what sizes the advertised receive window.
```

## Line 257

```
// --- lifecycle --------------------------------------------------------------
```

## Line 279

```
	// The same socket buffers this library gives its own socket
	// (DefaultSocketBufferSize, utp_socket.go). libutp leaves the socket to its
	// embedder, and an embedder that kept the kernel's default (208 KB here)
	// lost acknowledgements at 100 Mb/s whenever this thread was busy: the
	// comparison is of the two protocols, not of the two embeddings.
```

## Line 325

```
	// No defaults exist for these: libutp returns 0 when they are unset, which
	// would leave every timestamp and connection id zero.
```

## Line 390

```
// 20ms, so timeouts are checked promptly
```

## Line 411

```
			// libutp batches acks; without this they are only sent on the next
			// timeout tick, which throttles the sender badly.
```
