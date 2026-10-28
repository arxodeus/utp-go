# ack_window_test.go

Every comment in `ack_window_test.go` at e56c9aa, verbatim.

## Line 8 — func TestStaleAckIsIgnoredNotFatal

```
// An acknowledgement number this connection cannot match acknowledges
// nothing, and that is all it does.
//
// libutp computes how many packets an acknowledgement covers and discards the
// answer when it exceeds what is outstanding:
//
//	int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
//	// this happens when we receive an old ack nr
//	if (acks > conn->cur_window_packets) acks = 0;
//	                                        (utp_internal.cpp:1904-1907)
//
// This implementation used to reset the connection instead. CONFORMANCE.md
// recorded it as a divergence the packet-injection corpus cannot see: both
// sides answer such a packet with silence, so the transcripts agree while one
// connection is dead and the other is alive. That is why this test looks at
// what the connection does next rather than at what it emitted.
//
// The case is ordinary, not exotic. `invalidAckNum` already implements
// libutp's own pre-filter (utp_internal.cpp:1794-1807), which drops anything
// acking a packet never sent, so what reaches here is a *stale*
// acknowledgement -- one still inside libutp's three-packet tolerance below
// the last sequence number sent, but below everything this connection is
// tracking. A delayed or duplicated ST_STATE early in a connection is exactly
// that, and it used to be fatal.
```

## Line 50 — func TestStaleAckIsIgnoredNotFatal

```
// Two sequence numbers behind the connection's first: inside the window
	// invalidAckNum tolerates, outside everything sentPackets tracks. That
	// gap is the only way to reach this branch at all, which is worth
	// stating -- a test that could not reach it would prove nothing.
```

## Line 74 — func TestStaleAckIsIgnoredNotFatal

```
// And it still works: a real acknowledgement for the packet that is
	// genuinely outstanding is still acted on.
```

## Line 86 — func TestAckForAPacketNeverSentIsDropped

```
// The other half of the rule, and the reason the branch above is only ever
// reached by stale acknowledgements: anything acking a packet that was never
// sent is dropped before it gets there.
//
// libutp is unusually explicit about why -- "This would imply a spoofed
// address or a malicious attempt to attach the uTP implementation"
// (utp_internal.cpp:1794-1807) -- and dropping is all it does. Pinned here so
// that relaxing the reset above cannot quietly become an open door.
```

## Line 130 — func TestReadShutdownAcksWithoutBuffering

```
// The precise thing read_shutdown does, asserted directly rather than through
// a transfer: the acknowledgement number advances, and the advertised window
// does not shrink.
//
// libutp runs `conn->ack_nr++` outside the `!read_shutdown` guard
// (utp_internal.cpp:2344-2355). Everything the feature is worth follows from
// that one line: a peer whose data is being discarded is never told to stop
// sending, so it can drain its send buffer and complete its own close.
```

## Line 175 — func TestReadShutdownStillReorders

```
// Out-of-order packets still reorder correctly when they are being discarded:
// the sequence number has to be recorded even though the bytes are not.
```

## Line 192 — func TestReadShutdownStillReorders

```
// The gap first.
```

## Line 200 — func TestReadShutdownStillReorders

```
// Then what fills it: both should now be acknowledged.
```
