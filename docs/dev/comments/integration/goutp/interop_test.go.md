# integration/goutp/interop_test.go

Every comment in `integration/goutp/interop_test.go` at e56c9aa, verbatim.

## Line 21 — func quietLogger

```
// quietLogger is ours, quiet below critical.
```

## Line 26 — func puregoLogger

```
// puregoLogger is theirs, quiet below error.
```

## Line 40 — type pair

```
// pair is one of our sockets and one of purego's that can reach each other.
```

## Line 44 — type pair

```
// theirPeer is purego's socket as ours addresses it; ourAddr is ours as
	// purego dials it.
```

## Line 51 — func udpPair

```
// udpPair is a pair over loopback UDP.
```

## Line 72 — func (*pair).connect

```
// connect opens a connection between the two, with ours dialling or
// accepting.
```

## Line 118 — func readAllOurs

```
// readAllOurs reads ours to the end of the stream.
```

## Line 128 — func readAllTheirs

```
// readAllTheirs reads purego's to the end of the stream, or the deadline.
```

## Line 141 — func helloFirst

```
// helloFirst makes the dialler speak first when purego accepted and is to
// send: libutp completes an incoming connection only on the first data packet
// (utp_internal.cpp:2158-2161) and writes nothing before that
// (utp_writev, :3181), and purego, its port, likewise holds every write until
// the dialler has sent something -- measured, its Write still blocked after
// 10 s. Protocols over uTP have the dialler speak first, as BitTorrent's
// handshake does. This library completes the connection on the handshake
// instead (DEVIATIONS.md, "Completing an incoming connection"), so when ours
// accepts it may send first, and the tests let it.
```

## Line 166 — func transfer

```
// transfer sends payload one way on an open connection, the sender closing
// when done, and checks that the receiver read exactly it.
```

## Line 217 — func TestPuregoTransfer

```
// A transfer completes, intact, in every combination of who dials and who
// sends.
```

## Line 239 — func TestPuregoBothDirectionsAtOnce

```
// Both ends send at once on one connection, each half-closing when done,
// and each reads everything the other sent.
```

## Line 293 — func TestPuregoConcurrentConnections

```
// Many connections between the same two sockets at once, each carrying its
// own payload, routed and delivered without mixing them up.
```

## Line 325 — func TestPuregoSmallWrites

```
// Writes far smaller than a packet, as an interactive protocol makes them,
// arrive in order and whole: both engines coalesce small writes (Nagle), and
// the two must agree on where one packet's bytes end and the next's begin.
```

## Line 377 — func TestPuregoRequestResponse

```
// Request and response over a half-closed connection: the side that asks
// dials, sends its request and closes its writing half; the other reads to
// the end, answers and closes. The answer arrives on the half still open.
```
