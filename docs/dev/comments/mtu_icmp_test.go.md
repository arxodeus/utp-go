# mtu_icmp_test.go

Every comment in `mtu_icmp_test.go` at e56c9aa, verbatim.

## Line 8 — func TestMtuIcmpFragmentationNeededUsesTheRoutersFigure

```
// A router that says how big the next hop is should be believed, and believed
// exactly: the point of honouring ICMP at all is to skip the round trip that
// discovering the same limit by losing a probe would cost.
```

## Line 16 — func TestMtuIcmpFragmentationNeededUsesTheRoutersFigure

```
// A 1300-byte link carries 1300-20-8 = 1272 bytes of uTP datagram.
```

## Line 25 — func TestMtuIcmpFragmentationNeededUsesTheRoutersFigure

```
// libutp is explicit that this is the one case where the next size sent
	// is the ceiling rather than the midpoint below it (utp_internal.cpp:3088-3093).
```

## Line 36 — func TestMtuIcmpFragmentationNeededNeverRaisesTheCeiling

```
// The ceiling only ever comes down. A router quoting something larger than
// what is already known must not raise it -- libutp's min().
```

## Line 41 — func TestMtuIcmpFragmentationNeededNeverRaisesTheCeiling

```
// Bring the ceiling down first, the ordinary way.
```

## Line 52 — func TestMtuIcmpFragmentationNeededFallsBackWhenTheFigureIsUnusable

```
// "It might not be initialized or sent properly": routers predating RFC 1191
// send zero, and anything outside libutp's window is not to be trusted. The
// fallback is the binary search, with the gap halved so a probe finds the
// truth.
```

## Line 77 — func TestUdpPayloadForLinkMTUBounds

```
// The bounds themselves, stated once rather than inferred from the search's
// behaviour. 576 is IPv4's guaranteed reassembly size and the lowest libutp
// will act on; 0x2000 is where it stops believing the router.
```

## Line 89 — func TestUdpPayloadForLinkMTUBounds

```
// IPv6's minimum
```

## Line 90 — func TestUdpPayloadForLinkMTUBounds

```
// Ethernet
```

## Line 103 — func TestMtuIcmpFragmentationNeededLeavesRoomForTheHeaders

```
// The conversion is the one deliberate difference from libutp, so assert what
// it buys: a datagram of the size the search settles on must fit inside the
// link the router named, headers included. libutp's figure does not -- it is
// 28 bytes over, and the packet it sends next is dropped by the same router.
```

## Line 117 — func TestMtuIcmpFragmentationNeededLeavesRoomForTheHeaders

```
// And not needlessly small: within one header's worth of the limit.
```

## Line 124 — func TestMtuIcmpFragmentationNeededCanFinishTheSearch

```
// An ICMP report that lands inside the convergence threshold ends the search
// rather than leaving it probing forever: the same mtu_search_update tail the
// probe path uses.
```

## Line 130 — func TestMtuIcmpFragmentationNeededCanFinishTheSearch

```
// Raise the floor to just under the figure the router is about to give.
```

## Line 143 — func TestMtuIcmpFragmentationNeededBelowTheFloor

```
// A figure below the floor must not invert the search. libutp asserts
// mtu_floor <= mtu_ceiling and then computes a midpoint that would sit above
// the ceiling; with assertions off, that sends packets the path just said it
// cannot carry.
//
// It also settles below the 576-byte floor, which libutp never does: its
// floor is 576 of UDP payload and its ceiling is the router's link MTU, so on
// a link this narrow it keeps sending 576-byte payloads into a hop that
// carries 572. Going below the floor here is the consequence of converting
// the router's figure honestly, and 572 is what the link actually carries.
// The floor is a heuristic -- "less would not pass TCP" -- not a guarantee.
```

## Line 178 — func TestMtuIcmpFragmentationNeededSavesProbes

```
// What the mechanism is for, stated as a saving: against a path that silently
// drops anything over 1300 bytes but whose router says so, the ICMP-informed
// search reaches a usable size without losing probes to find it.
```

## Line 197 — func TestMtuIcmpFragmentationNeededSavesProbes

```
// The router that dropped it told us why.
```
