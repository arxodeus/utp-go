# keepalive_timing_test.go

Every comment in `keepalive_timing_test.go` at e56c9aa, verbatim.

## Line 9 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// The keep-alive leaves one interval after the last packet, not up to two.
//
// libutp checks `current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every
// timeout pass (utp_internal.cpp:1271-1274). This library checked the same
// condition only on a ticker at the interval, counted from connection start,
// so a connection that went quiet 5 seconds after a tick failed the check at
// the next one and sent at the one after: 53 seconds of silence where libutp
// allows 29. TestKeepAlive could not see that -- it gives the keep-alive six
// intervals to turn up -- so this runs on the virtual clock at libutp's real
// interval and asserts the instant.
```

## Line 25 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// well away from any multiple of the interval
```

## Line 26 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// The instant is exact on the virtual clock; this only has to be
		// finer than the gap between right and wrong, which is 24 seconds.
```

## Line 57 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// The read and socket event loops and the retransmission wheel register
	// when the socket is built; the connection's own loop only
	// once the SYN arrives.
```

## Line 77 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// Speak once, 5 seconds in: the acknowledgement for this is the last
	// packet the connection sends before it goes quiet.
```

## Line 112 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// Each keep-alive is itself the last packet sent, so the next is one
		// interval after it.
```

## Line 116 — func TestKeepAliveLeavesOneIntervalAfterTheLastPacket

```
// Answer it, as a live peer does. The answer must not itself draw a
		// packet, or it would move lastSent.
```

## Line 128 — func TestKeepAliveSelectiveAckMatchesItsAckNumber

```
// A keep-alive sent while a gap is open names the packets that really arrived.
//
// libutp's keep-alive is an acknowledgement one behind the real one, built by
// decrementing ack_nr before send_ack (utp_internal.cpp:834-844), so its
// selective ack is computed from the decremented ack_nr as well (:804-808)
// and the peer, reading bit i as ack_nr + 2 + i against the header (:1441-),
// finds the right packets. This library built the state packet first, with
// its selective ack computed from the real ack number, and then decremented
// the header: every bit was read one packet early, so the peer would have
// taken a packet it still owed as delivered, freed it, and never sent it
// again -- and this end would have waited for it forever. Found reading
// send_keep_alive line by line (the audit in LIBUTP-AUDIT.md).
```

## Line 177 — func TestKeepAliveSelectiveAckMatchesItsAckNumber

```
// peerSeq+1 never arrives; peerSeq+2 does. The acknowledgement stays at
	// peerSeq, with peerSeq+2 selectively acknowledged.
```

## Line 212 — func TestIdleTimeoutRunsFromTheLastPacketHeard

```
// The idle timeout runs from the last packet heard, to the instant.
//
// Activity used to reset the idle timer on every packet. It now only records
// the time, and the timer, when it fires, arms again for what is left
// (connection.lastActivity). So the timer set at the handshake fires at 10
// seconds, finds a packet heard at 6, and must neither close the connection
// then nor let it outlive 16.
```
