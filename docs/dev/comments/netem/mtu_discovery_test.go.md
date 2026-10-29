# netem/mtu_discovery_test.go

Every comment in `netem/mtu_discovery_test.go` at e56c9aa, verbatim.

## Line 12 — func mtuTransfer

```
// mtuTransfer runs one transfer over a link with the given config and returns
// the sender's last observed MTU search state, plus the link's stats.
```

## Line 89 — func TestMtuSearchConvergesWhenThePathAllowsIt

```
// Path-MTU discovery, against a path that actually limits size.
//
// Until the link model grew an MTU, every emulated path carried any datagram
// however large, so the search always converged on whatever ceiling it was
// given and the half of it that *lowers* the ceiling was never exercised end
// to end.
//
// This is the case that works: the path carries more than the search will ever
// ask for, so the search climbs to its own ceiling and settles.
```

## Line 122 — func TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice

```
// A path whose MTU is below the size the search has already adopted.
//
// STILL SKIPPED, but for a narrower reason than it was.
//
// The search raises the size it sends at whenever a probe is acknowledged. If
// the path limit sits between two probe sizes, the next size it adopts cannot
// arrive -- and because every data packet is then built at that size, nothing
// arrives at all.
//
// This used to say the search could never come back, because the ceiling only
// falls when a probe times out as the *only* packet outstanding
// (utp_internal.cpp:1152-1160), which a stalled connection never reaches. That
// was true of this library and is no longer: libutp's other route, three
// duplicate acknowledgements pointing at the packet before the probe
// (:1927-1940), is implemented now, and it works while the window is full.
// Measured on this same 1100-byte link, with the skip lifted:
//
//	before  search parks at floor=576 current=1191 ceiling=1400 -- above the path
//	after   search comes down to floor=982 current=1083 ceiling=1184
//
// So the search does recover, and the sizes it builds from then on fit. What
// does not recover is the transfer: it still delivers a few kilobytes of a
// megabyte and the reader times out after 60 seconds, which is why this stays
// skipped. uTP numbers packets, not bytes, so the packets already built at
// 1191 bytes cannot be re-cut smaller -- that would renumber everything behind
// them -- and the peer will never acknowledge packets it cannot receive.
// libutp has the same constraint and gets away with it by not setting
// don't-fragment on ordinary data ("now we need it to fragment just to get it
// through", :898-905), leaving the router to fragment what it cannot forward
// whole. This emulated link refuses oversized datagrams outright, which is
// what an IPv6 path does.
//
// The remaining ceiling matters too: 1184 is still above the 1100-byte path,
// so the search could climb back. An ICMP report brings it to exactly the
// path -- see TestIcmpBringsTheSearchWithinThePath, where that is now the
// difference the report makes.
//
// It matters in practice: a path MTU below 1400 is ordinary (PPPoE at 1492,
// most VPN and tunnel paths), and a BitTorrent client on one would stall a
// connection it had already put packets on.
```

## Line 187 — func TestPathMTUReportPreventsTheStall

```
// The stall above, prevented rather than recovered from.
//
// TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice is skipped because
// once the connection has stalled on a size the path will not carry, neither
// this library nor libutp gets the transfer back: the packets already built
// are too big, uTP numbers packets rather than bytes so they cannot be re-cut,
// and the peer will never acknowledge what it cannot receive.
//
// The search itself does recover, since the duplicate-acknowledgement route
// lowers the ceiling while the window is full. That is not enough, which is
// the point of this case.
//
// The way out is to never adopt that size, which is what libutp's
// UTP_GET_UDP_MTU callback is for -- it sets the ceiling from the local
// interface before the first packet goes out. This library had no equivalent
// until utp.PathMTUProvider; here the endpoint answers it, as a host
// behind a tunnel would.
```

## Line 218 — func TestPathMTUReportPreventsTheStall

```
// The sender knows what its own interface carries. The receiver is not
	// told, and does not need to be: it sends only acknowledgements.
```
