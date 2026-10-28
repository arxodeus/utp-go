# peer_window_test.go

Every comment in `peer_window_test.go` at e56c9aa, verbatim.

## Line 9

```
// The peer's receive window, as libutp's sender spends it.
//
// flush_packets sends the next queued packet only while !is_full(), and
// is_full with no argument refuses when cur_window + packet_size >
// min(max_window, opt_sndbuf, max_window_user) (utp_internal.cpp:933-936,
// :956, :974). The conformance corpus cannot isolate this against libutp at
// connection start -- libutp's congestion window is one packet there (:2567),
// which holds a second packet back whatever the peer advertises, and its first
// packet is 1452 bytes where ours is 962 (KNOWN-LIMITATIONS.md, M6) -- so the
// rule is asserted here, on our side alone.
```

## Line 20 — func sendAndCount

```
// sendAndCount queues data and returns the payload sizes that went out.
```

## Line 42 — func TestInFlightBytesSpendThePeersWindow

```
// Bytes already in flight spend the peer's window.
```

## Line 66 — func TestNothingIsSentIntoLessThanAPacketOfRoom

```
// Nothing goes out into less than a full packet of room, and nothing is cut
// down to fit it -- even a write smaller than the room.
```

## Line 87 — func TestAWidePeerWindowSendsEveryPacket

```
// The control for both: a window with room for everything sends everything.
// The send buffer is widened to hold three whole packets: at 2048 bytes the
// third was a short tail, which the Nagle rule holds back (TestNagle*).
```

## Line 104 — func TestRecvBufferDropsCountsRefusalsOnly

```
// RecvBufferDrops counts a packet refused for want of room, and not a
// duplicate of one already held.
```
