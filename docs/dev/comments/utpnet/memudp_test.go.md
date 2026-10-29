# utpnet/memudp_test.go

Every comment in `utpnet/memudp_test.go` at e56c9aa, verbatim.

## Line 10 — type memNet

```
// memNet is an in-memory UDP network: datagrams written to an address are
// delivered to the memUDP bound to it, and nothing else. It exists so that
// Socket can be run end to end with IPv6 addresses on a host whose kernel has
// no IPv6 -- the machine this was developed on answers every AF_INET6 socket
// with "address family not supported by protocol".
//
// What it does not stand in for is the kernel: no socket options, no
// dual-stack mapping, no path MTU. It exercises everything above the socket.
```

## Line 21 — type memNet

```
// mtu, when non-zero, is the largest datagram the network carries.
	// Anything larger vanishes without a word, as it does on an IPv6 path
	// whose routers' ICMPv6 "packet too big" is filtered.
```

## Line 29 — func (*memNet).listen

```
// listen binds addr, which must parse as a UDP address.
```

## Line 56 — type memUDP

```
// memUDP is one bound address on a memNet.
```

## Line 67 — type memUDP

```
// changed wakes a blocked read when the deadline moves, which is how
	// Socket.Close unblocks its reader.
```

## Line 108 — func (*memUDP).WriteToUDP

```
// WriteToUDP delivers to whatever is bound at addr, and like UDP says
// nothing if nothing is, or if its queue is full.
```
