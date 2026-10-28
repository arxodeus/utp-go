# native/libutp/libutp.go

Every comment in `native/libutp/libutp.go` at e56c9aa, verbatim.

## Line 1 — package libutp

```
//go:build cgo
```

## Line 3 — package libutp

```
// Package libutp wraps the vendored C libutp so Go tests can talk to the
// reference implementation over a real UDP socket.
//
// This exists for one reason: everything else in this repository is
// conformance by citation -- code read against utp_internal.cpp and matched by
// hand. That is worth a lot, but it is not evidence that a real libutp peer
// will complete a transfer with us. This package is what turns the claim into
// a test.
//
// See VENDOR.md for the pinned upstream commit and how to refresh it.
```

## Line 15

```
/*
#cgo CXXFLAGS: -DPOSIX -fno-exceptions -fno-rtti -Wno-sign-compare -fpermissive -O2
#cgo LDFLAGS: -lstdc++

#include <stdlib.h>
#include "bridge.h"
*/
```

## Line 32 — type State

```
// State mirrors the peer's connection state.
```

## Line 63 — var errorNames

```
// libutp's error codes, from utp.h.
```

## Line 66 — type Peer

```
// Peer is one libutp endpoint carrying at most one connection.
```

## Line 74 — func NewPeer

```
// NewPeer binds a libutp endpoint to 127.0.0.1 on the given port, or an
// arbitrary free port if it is 0, and starts its event loop.
```

## Line 84 — func NewPeer

```
// Blocks until Shutdown. libutp is single-threaded, so this goroutine
		// owns every call into it; the bridge serialises the rest.
```

## Line 91 — func (*Peer).Port

```
// Port reports the bound UDP port.
```

## Line 94 — func (*Peer).Listen

```
// Listen makes the peer accept the next incoming connection. Until it is
// called, incoming SYNs are refused.
```

## Line 98 — func (*Peer).Connect

```
// Connect starts an outgoing connection to 127.0.0.1 on the given port.
```

## Line 106 — func (*Peer).State

```
// State reports the current connection state.
```

## Line 109 — func (*Peer).Err

```
// Err reports the last libutp error, if the peer is in StateError.
```

## Line 122 — func (*Peer).Write

```
// Write queues data for sending. It never blocks: the queue grows as needed
// and libutp drains it as its window allows. Use PendingWrite to see how much
// has not yet been handed over.
```

## Line 136 — func (*Peer).PendingWrite

```
// PendingWrite reports bytes queued but not yet accepted by libutp.
```

## Line 139 — func (*Peer).Read

```
// Read copies out received data. It returns 0 with no error when nothing has
// arrived yet -- it does not block.
```

## Line 149 — func (*Peer).CloseStream

```
// CloseStream closes the uTP connection once everything queued has been handed
// to libutp. The peer itself stays alive until Shutdown.
```

## Line 153 — func (*Peer).Shutdown

```
// Shutdown stops the event loop and releases the peer.
```

## Line 162

```
// --- helpers for tests ------------------------------------------------------
```

## Line 164 — func (*Peer).WaitState

```
// WaitState blocks until the peer reaches one of the given states, or the
// timeout expires. It reports the state it saw.
```

## Line 183 — func (*Peer).ReadFull

```
// ReadFull collects exactly n bytes, or fails on timeout.
```

## Line 208 — func (*Peer).WaitDrained

```
// WaitDrained blocks until everything queued has been handed to libutp.
```
