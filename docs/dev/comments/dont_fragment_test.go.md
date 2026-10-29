# dont_fragment_test.go

Every comment in `dont_fragment_test.go` at e56c9aa, verbatim.

## Line 10 — type dfConn

```
// dfConn is a Conn that records how each datagram was asked to be sent.
```

## Line 13 — type dfConn

```
// sends records one entry per datagram: true if it was sent through
	// WriteToDontFragment, false if through the ordinary WriteTo.
```

## Line 17 — type dfConn

```
// unsupported makes WriteToDontFragment refuse, as a platform with no
	// such socket option does.
```

## Line 23 — func (*dfConn).ReadFrom

```
// never reads; these tests only look at the write side
```

## Line 59 — func TestDontFragmentReachesTheConn

```
// The bit reaches a Conn that can honour it, and only for a probe.
```

## Line 86 — func TestDontFragmentUnsupportedStillSends

```
// A Conn that implements the interface but cannot honour it right now must
// still get the datagram. Losing the probe instead would turn a missing socket
// option into a hole in the sequence space.
```

## Line 111 — func TestDontFragmentConnWithoutTheInterface

```
// A Conn that never heard of the interface is unaffected.
```

## Line 114 — func TestDontFragmentConnWithoutTheInterface

```
// path_mtu_test.go's plainConn embeds the Conn *interface*, so only
	// ReadFrom, WriteTo and Close are promoted: the DontFragmentWriter method
	// on the value inside is invisible to a type assertion. That is precisely
	// the Conn this case is about -- the libutp driver, an emulated network,
	// anything written before this interface existed.
```

## Line 132 — func TestDontFragmentTravelsOnTheSocketEvent

```
// The hop between the connection and the socket: a packet emitted as a probe
// must carry the bit on its socket event, and an ordinary one must not.
//
// The tests above exercise the last hop, from the socket to the Conn.
// This is the one before it, and between them they cover the whole path from
// mtuSearch.eligibleProbe to WriteToDontFragment.
```

## Line 159 — func TestDontFragmentUnsupportedIsMatchable

```
// ErrDontFragmentUnsupported must be matchable with errors.Is through a wrap,
// because writeDatagram tests it that way.
```
