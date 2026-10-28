# native/libutp/bridge_test.go

Every comment in `native/libutp/bridge_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp

```
//go:build cgo
```

## Line 11

```
// Validate the bridge before trusting any interop result. If libutp cannot
// talk to itself through this wrapper, an interop failure would say nothing
// about the Go implementation.
```

## Line 87 — func TestBridgeRefusesWhenNotListening

```
// A peer that is not listening must refuse an incoming connection, which is
// what the firewall callback is for.
```

## Line 101 — func TestBridgeRefusesWhenNotListening

```
// No Listen() call.
```

## Line 108 — func TestBridgeRefusesWhenNotListening

```
// The client should end up erroring rather than connecting.
```
