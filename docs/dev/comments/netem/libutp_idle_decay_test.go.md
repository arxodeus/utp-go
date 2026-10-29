# netem/libutp_idle_decay_test.go

Every comment in `netem/libutp_idle_decay_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 18 — func TestLibutpIdleWindowDecay

```
// What real libutp does to its congestion window while a connection sits idle.
//
// libutp checks one retransmission deadline per socket rather than one timer
// per packet, and `rto_timeout` is never cleared when the window empties: it
// is set when a packet goes into an empty window (utp_internal.cpp:997), reset
// on every acknowledged packet (:1389), and re-armed on every expiry (:1204).
// So once the last packet is acknowledged the deadline keeps ticking, and when
// it passes with nothing outstanding the idle branch runs:
//
//	if ((cur_window_packets == 0) && ((int)max_window > packet_size)) {
//	    // we don't have any packets in-flight, even though
//	    // we could. This implies that the connection is just
//	    // idling. No need to be aggressive about resetting the
//	    // congestion window. Just let it decay by a 3:rd.
//	    max_window = max(max_window * 2 / 3, size_t(packet_size));
//	                                        (utp_internal.cpp:1216-1222)
//
// `retransmit_count` is only incremented when something *is* outstanding
// (:1240), so this repeats without ever killing the connection: the window
// decays by a third at each expiry while the RTO doubles, so the decays land
// at one, three, seven, fifteen RTOs and the window falls geometrically.
//
// This library cannot do that. Its retransmission timers are armed per packet
// and there is none to arm when nothing is outstanding, so an idle connection
// keeps whatever window it last had. Whether that is worth changing depends on
// what libutp actually does rather than on what its source appears to say,
// which is what this measures.
//
// The window is not readable from outside libutp in an ordinary build --
// `max_window` is private and `utp_socket_stats` does not report it -- so it
// is measured the way a peer would see it: idle for a while, then write a
// burst, and count what comes out before anything is acknowledged. That
// first flight is the window, rounded down to whole packets. A
// libutp_debuglog build also reads the window itself, and checks the two
// agree.
```

## Line 59 — func TestLibutpIdleWindowDecay

```
// The first flight is how this test reads libutp's window without
		// the debug build. libutp sends a packet only while the whole of it
		// fits (is_full, utp_internal.cpp:956), so the flight is the window
		// rounded down to whole packets: within one packet of it, never over.
```

## Line 83 — func TestLibutpIdleWindowDecay

```
// libutp has to have decayed, or there is nothing here to compare against
	// and this test is measuring the harness rather than the reference.
```

## Line 93 — func TestLibutpIdleWindowDecay

```
// Three decays by a third, at one, three and seven RTOs, predicts
	// (2/3)^3 = 30%. Loose, because which decays land inside the window
	// depends on where the RTO floor sits.
```

## Line 102 — func TestLibutpIdleWindowDecay

```
// And ours has to decay the same way. This is the assertion the test was
	// built for: it began by pinning the *divergence* -- ours held 100% of its
	// window where libutp kept 29% -- and failed, as intended, the moment the
	// decay was implemented.
	// Compared as a decay *rule*, not as a ratio.
	//
	// Comparing the ratios directly is what a first version did, and it fails
	// one run in three for a reason that is not a divergence: how many
	// expiries fit in the idle period depends on where the RTO floor lands,
	// so a run may take two decays where another takes three -- 44% against
	// 30% -- while applying exactly the same rule. Converting each ratio to
	// the number of thirds it represents compares the rule and tolerates the
	// count, and still fails on a genuinely different rule: three halvings
	// would read as 5.1 steps against libutp's 3.
```

## Line 128 — func TestLibutpIdleWindowDecay

```
// Neither should fall below one packet, which is the floor libutp clamps
	// to in the same expression.
```

## Line 135 — func ourWindowAcrossIdle

```
// ourWindowAcrossIdle reports this library's congestion window after a bulk
// transfer and again after `idle` of silence.
//
// Ours is read directly from the metrics rather than inferred from a first
// flight, because it can be: libutp's `max_window` is private and
// `utp_socket_stats` does not report it, which is the only reason that side is
// measured the roundabout way.
```

## Line 209 — func ourWindowAcrossIdle

```
// Idle. An idle connection produces no metrics samples at all, because
	// nothing wakes its event loop -- which is the mechanism behind the
	// divergence as much as the missing timer is. So the window afterwards is
	// read from the first sample the next write produces.
```

## Line 237 — const maxLibutpPayload

```
// maxLibutpPayload bounds a libutp data packet's payload on these links: an
// Ethernet frame's worth, larger than any packet libutp sends here.
```

## Line 241 — func libutpFirstFlightAfterIdle

```
// libutpFirstFlightAfterIdle drives real libutp as the sender: a bulk transfer
// to grow its window, then `idle` of silence, then a burst. It reports how
// many bytes libutp put on the wire in the burst's first flight.
//
// window is libutp's congestion window read directly as the burst began, and
// direct whether it could be: only in a libutp_debuglog build (see
// libutp.Driver.EnableWindowLog).
```

## Line 271 — func libutpFirstFlightAfterIdle

```
// Stop and join the pump before the C driver is freed.
```

## Line 298 — func libutpFirstFlightAfterIdle

```
// The receiver is one of ours, reading continuously so its advertised
	// window never becomes the limit.
```

## Line 303 — func libutpFirstFlightAfterIdle

```
// pumpMu is held by the pump for each pass and by the test around the
	// burst, so the burst's packets can be counted before they are sent.
```

## Line 356 — func libutpFirstFlightAfterIdle

```
// Wait for the bulk transfer to be delivered and quiet down.
```

## Line 364 — func libutpFirstFlightAfterIdle

```
// The burst. libutp sends from inside the write that queues it, as much
	// as its window allows (utp_writev, utp_internal.cpp:3200-3212), and no
	// acknowledgement can arrive while the driver is in that call -- so what
	// the write emits is the first flight, exactly. It is counted before the
	// pump sends it.
	//
	// It used to be timed instead: everything emitted within 40 ms of the
	// burst's first packet. Under load -- the race detector, or another test
	// binary on the machine -- the pump could stall past that window between
	// the first packet and the rest, and the flight read as one packet:
	// 1,452 bytes where libutp's window, read directly, was 16,534.
```

## Line 377 — func libutpFirstFlightAfterIdle

```
// Where the build can read the window itself, it is read as libutp first
	// asks whether a packet of the burst fits.
```

## Line 393 — func libutpFirstFlightAfterIdle

```
// Only data packets count towards a flight.
```

## Line 404 — type receivingEndpoint

```
// receivingEndpoint is one of our sockets accepting a connection and reading
// continuously, used as the far end for a libutp sender.
```

## Line 439 — func (*receivingEndpoint).waitForBytes

```
// Let the last acknowledgements settle.
```
