# path_mtu_test.go

Every comment in `path_mtu_test.go` at e56c9aa, verbatim.

## Line 11 — type fakePathMTU

```
// fakePathMTU is a Conn that answers PathMTU, so the composition rule can be
// tested without depending on whatever interfaces this machine happens to
// have.
```

## Line 23 — func (*fakePathMTU).LocalAddr

```
// LocalAddr is forwarded explicitly: Socket.LocalAddr duck-types for it,
// and embedding the Conn interface promotes only the methods Conn declares.
```

## Line 27 — type plainConn

```
// A Conn with no PathMTU method at all: the ordinary case, and the one that
// must be completely unaffected.
```

## Line 40 — func TestPathMTUCeilingOnlyLowers

```
// The composition rule, which is the whole of the design decision: a
// discovered path MTU may lower the configured ceiling and may never raise
// it.
//
// libutp takes get_udp_mtu as the ceiling outright. Loopback reports 65536
// and a jumbo-frame link 9000, and adopting either would put this library on
// a probe schedule nothing has measured it at. Raising the ceiling is a
// separate decision from fixing the case where 1400 is too big.
```

## Line 84 — func TestPathMTUCeilingOnlyLowers

```
// A nil peer is not a reason to guess.
```

## Line 90 — func TestPathMTUSetsTheCeilingUnderARaisedMaximum

```
// With MaxPacketSize raised, the report sets the ceiling, as libutp's
// get_udp_mtu does (mtu_reset, utp_internal.cpp:1316): how an embedder gets
// libutp's behaviour with a callback that reports the interface.
```

## Line 100 — func TestDatagramSizeForInterfaceMTU

```
// The header arithmetic, stated once rather than inferred from behaviour.
```

## Line 111 — func TestDatagramSizeForInterfaceMTU

```
// Ethernet
```

## Line 112 — func TestDatagramSizeForInterfaceMTU

```
// WireGuard's default
```

## Line 113 — func TestDatagramSizeForInterfaceMTU

```
// IPv6's floor
```

## Line 114 — func TestDatagramSizeForInterfaceMTU

```
// IPv6 over Ethernet: a bigger header
```

## Line 115 — func TestDatagramSizeForInterfaceMTU

```
// loopback
```

## Line 116 — func TestDatagramSizeForInterfaceMTU

```
// exactly the absolute floor
```

## Line 117 — func TestDatagramSizeForInterfaceMTU

```
// one below it
```

## Line 118 — func TestDatagramSizeForInterfaceMTU

```
// a link that cannot carry 576 of payload
```

## Line 130 — func TestPathMTUDiscoveryAgreesWithTheInterface

```
// Discovery against this machine, whatever it has. The value cannot be
// asserted -- it depends on the host -- but its relationship to the interface
// it came from can.
```

## Line 143 — func TestPathMTUDiscoveryAgreesWithTheInterface

```
// Find the loopback interface and check the arithmetic end to end.
```

## Line 170 — func TestNarrowPathLowersTheConnectionCeiling

```
// End to end: a socket whose Conn reports a narrow path must build its
// connections with a correspondingly narrow ceiling, and must still work.
//
// The ceiling is read back through ConnectionMetrics, which is the only view
// of the search from outside.
```

## Line 180 — func TestNarrowPathLowersTheConnectionCeiling

```
// The narrow-path answer is stood in front of a real UDP socket before
	// the uTP socket is built over it. Wrapping s.socket afterwards would be
	// a write racing the read loop that is already reading it -- which the
	// race detector duly reported when this test did that.
```

## Line 184 — func TestNarrowPathLowersTheConnectionCeiling

```
// an IPv6 minimum link
```

## Line 210 — func TestNarrowPathLowersTheConnectionCeiling

```
// Collected under a lock rather than through a channel: the observer runs
	// on the connection's goroutine for as long as the connection lives, so
	// there is no moment at which the test can safely close a channel it is
	// still writing to.
```

## Line 247 — func TestNarrowPathLowersTheConnectionCeiling

```
// The caller's config must come back untouched: it is theirs, and may be
	// reused for the next connection.
```

## Line 301 — func TestIPv6PeersGetTheIPv6SafeCeiling

```
// An IPv6 peer gets libutp's IPv6 ceiling, 1232 (UDP_TEREDO_MTU,
// utp_utils.cpp:230-235); anything else keeps what was configured.
```
