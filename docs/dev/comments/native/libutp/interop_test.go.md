# native/libutp/interop_test.go

Every comment in `native/libutp/interop_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 19

```
// The M7 interoperability gate.
//
// Everything else in this repository is conformance by citation: code read
// against utp_internal.cpp and matched by hand. These tests are the only
// evidence that a real libutp peer -- the thing every BitTorrent client in the
// wild actually runs -- will complete a transfer with this implementation.
//
// Both directions matter. A bug in connection-id allocation, for instance,
// can leave one role working and the other unroutable.
```

## Line 45 — func bindGoSocket

```
// bindGoSocket binds one of our sockets to an arbitrary free port and reports
// the port it got.
```

## Line 49 — func bindGoSocket

```
// Find a free port by binding a throwaway UDP socket.
```

## Line 66

```
// --- our implementation as the initiator ------------------------------------
```

## Line 83 — func TestInteropGoInitiatorLibutpResponder

```
// Our initiator sends the SYN carrying cid.Recv; libutp derives its own
	// ids from it. Getting this relationship wrong is what makes an
	// implementation unroutable to half its peers, so it is the first thing
	// this test exercises.
```

## Line 129

```
// --- our implementation as the responder ------------------------------------
```

## Line 147 — func TestInteropLibutpInitiatorGoResponder

```
// libutp chooses its own random connection id, so the responder cannot
	// know it in advance: this is the path that accepts whatever SYN arrives.
```

## Line 170 — func TestInteropLibutpInitiatorGoResponder

```
// Give the accept a moment to register before the SYN arrives.
```
