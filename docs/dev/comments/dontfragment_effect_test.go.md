# dontfragment_effect_test.go

Every comment in `dontfragment_effect_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux || darwin || freebsd || windows
```

## Line 11 — func TestDontFragmentActuallySetsTheBit

```
// The socket option has an effect, on a real socket, observed rather than
// assumed.
//
// Every other test in this area checks that the *request* travels: that a
// probe is marked, that the mark reaches the Conn, that a Conn which cannot
// honour it still sends. None of them can tell whether setDontFragment
// actually does anything, and a setsockopt that silently succeeded on the
// wrong option would pass all of them.
//
// This one puts a datagram larger than the outbound interface's MTU on a real
// socket. Without the bit the kernel fragments it and the send succeeds. With
// the bit there is nothing the kernel may do but refuse, and the send fails
// with EMSGSIZE. The contrast is the assertion: both halves must hold, so
// the test fails if the option stops working *or* if the environment stops
// being one where the difference is visible.
```

## Line 27 — func TestDontFragmentActuallySetsTheBit

```
// A destination off-link, so the route leaves by a real interface rather
	// than loopback. Nothing has to be listening: the send either is refused
	// by the local kernel or is handed to the interface, and both answers
	// arrive before anything leaves the machine.
```

## Line 50 — func TestDontFragmentActuallySetsTheBit

```
// Without the bit: the kernel fragments, and the send succeeds.
```

## Line 62 — func TestDontFragmentActuallySetsTheBit

```
// With the bit: the same datagram must be refused.
```

## Line 74 — func TestDontFragmentActuallySetsTheBit

```
// And the bit must not break ordinary sends that do fit.
```

## Line 79 — func TestDontFragmentActuallySetsTheBit

```
// Clearing it must restore fragmentation, or every later packet on this
	// socket would be unfragmentable -- the thing libutp is explicit about
	// not wanting (utp_internal.cpp:898-905).
```

## Line 90 — func TestUdpConnWriteToDontFragment

```
// The same contrast through the public path: udpConn.WriteToDontFragment
// against udpConn.WriteTo.
```

## Line 119 — func TestUdpConnWriteToDontFragment

```
// The bit must be cleared again afterwards, including after that failure.
	// If it leaked, this send would fail too.
```
