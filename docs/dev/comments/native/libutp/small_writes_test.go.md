# native/libutp/small_writes_test.go

Every comment in `native/libutp/small_writes_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 15 — func spinUntil

```
// spinUntil waits for t without sleeping past it: Go timers wake about 1.1 ms
// late on an idle runtime.
```

## Line 31 — func smallWrites

```
// smallWrites sends count messages of size bytes, one every interval, from
// libutp or from this library, to this library's receiver, and reports what
// went over the data path and how long each message took to arrive.
```

## Line 101 — func smallWrites

```
// The bridge tells libutp its path carries 1472-byte datagrams
		// (bridge.cpp, cb_get_udp_mtu); the same ceiling here, so that the
		// packets compared are the same size.
```

## Line 131 — func smallWrites

```
// dataEv is the relay's to append to until it has stopped.
```

## Line 147 — func TestSmallWritesAgainstLibutp

```
// TestSmallWritesAgainstLibutp writes 2,000 messages of 100 bytes, one every
// 0.5 ms, over a 20 ms round trip -- a peer sending protocol messages faster
// than they can be acknowledged -- from libutp and then from this library,
// to this library's receiver, and counts the data packets each put on the
// path.
//
// libutp coalesces them: a short packet waits while anything is
// unacknowledged and later writes join it (flush_packets,
// utp_internal.cpp:974-982; write_outgoing_packet, :1013-1023). Measured, with
// both at the 1472-byte datagram the bridge reports to libutp: 139 packets
// from each. The gate is our count within smallWritesSlack of libutp's.
//
// With NoDelay, or without the Nagle rule, this library sent 1,920-2,000
// packets, one per message, and fails it.
//
// Latency is logged and not gated: libutp's includes its bridge's loop, which
// waits up to 20 ms for the socket when a write arrives (bridge.cpp), so its
// tail is not libutp's alone. Measured, the median message took 15.3 ms from
// libutp and 14.7 ms from this library.
```

## Line 188 — const smallWritesSlack

```
// smallWritesSlack is how many more packets than libutp's this library may
// send. Measured equal; the slack is for where the 0.5 ms writes fall against
// the round trip on a loaded machine.
```
