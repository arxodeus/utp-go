# dontfragment_linux.go

Every comment in `dontfragment_linux.go` at e56c9aa, verbatim.

## Line 8 — func setDontFragment

```
// setDontFragment turns the IPv4 don't-fragment bit, and its IPv6 equivalent,
// on or off for the next datagram sent on this socket.
//
// Linux has no per-datagram control message for this -- it is a socket option
// on every platform that offers it at all -- so the caller sets it, sends one
// datagram, and clears it. That is only safe because the socket lets no other
// write through meanwhile (Socket.writeDatagram).
//
// IP_PMTUDISC_PROBE rather than IP_PMTUDISC_DO. Both set the bit; the
// difference is whose path-MTU estimate wins. DO defers to the kernel's
// cached estimate for the route and fails the send with EMSGSIZE when the
// datagram exceeds it. PROBE sets the bit and sends the datagram regardless.
//
// This library runs its own search, with its own floor, ceiling and history
// (mtu.go), and libutp does the same. A kernel estimate refusing the send
// would silently cap that search at whatever the kernel had already learned,
// which is the opposite of probing: the point of a probe is to find out
// whether a size works, not to be told what the kernel currently believes.
```

## Line 39 — func setDontFragment

```
// One of the two always fails: a socket has one address family, and the
	// option for the other is not recognised on it. Succeeding if either
	// worked is what makes this correct for a v4 socket, a v6 socket and a
	// dual-stack v6 socket carrying v4-mapped addresses alike, without having
	// to work out which it is.
```
