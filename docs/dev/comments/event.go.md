# event.go

Every comment in `event.go` at e56c9aa, verbatim.

## Line 8 — const streamIncoming, streamShutdown, streamCloseWrite, streamCloseRead, stre...

```
// streamCloseWrite wakes the event loop for a half-close. It is separate
	// from streamShutdown because that one means "finished entirely" and sets
	// the flag to prove it; CloseWrite only needs the loop to notice that the
	// sending side is done, and must not be mistaken for a full close.
```

## Line 13 — const streamIncoming, streamShutdown, streamCloseWrite, streamCloseRead, stre...

```
// streamCloseRead wakes the event loop for the other half-close: this end
	// has finished reading. libutp's utp_shutdown(SHUT_RD), which sets
	// read_shutdown (utp_internal.cpp:3409-3411).
```

## Line 17 — const streamIncoming, streamShutdown, streamCloseWrite, streamCloseRead, stre...

```
// streamICMP carries an ICMP report about a packet this connection sent.
	// The socket parses the quoted uTP header and finds the connection; the
	// connection decides what the report means, which is where libutp puts
	// the decision too (utp_process_icmp_fragmentation and
	// utp_process_icmp_error, utp_internal.cpp:3079-3150).
```

## Line 25 — type icmpKind

```
// icmpKind distinguishes libutp's two ICMP entry points.
```

## Line 29 — const icmpFragmentationNeeded, icmpUnreachable

```
// icmpFragmentationNeeded is ICMP type 3 code 4, or ICMPv6 "packet too
	// big": the datagram was larger than some hop would carry.
	// utp_process_icmp_fragmentation.
```

## Line 33 — const icmpFragmentationNeeded, icmpUnreachable

```
// icmpUnreachable is any ICMP error that should tear the connection
	// down. utp_process_icmp_error.
```

## Line 38 — type icmpNotice

```
// icmpNotice is one ICMP report, already matched to a connection.
```

## Line 41 — type icmpNotice

```
// NextHopMTU is the router's figure from a fragmentation-needed message:
	// the largest IP datagram the next hop will carry. Zero when the router
	// did not supply one, which older routers do not.
```

## Line 57 — type streamEvent

```
// ICMP is set on streamICMP events and nil otherwise.
```

## Line 61 — type socketEvent

```
// socketEvent represents events related to a socket.
```

## Line 66 — type socketEvent

```
// DontFragment marks this datagram as an MTU probe, to be sent with
	// fragmentation disabled if the Conn can do that. libutp carries the same
	// bit from send_packet to its embedder's sendto callback as
	// UTP_UDP_DONTFRAG (utp_internal.cpp:928). See DontFragmentWriter.
```

## Line 81 — func newOutgoingProbeSocketEvent

```
// newOutgoingProbeSocketEvent is newOutgoingSocketEvent for a datagram the
// path-MTU search is using as a probe.
```

## Line 92 — func newShutdownSocketEvent

```
// newShutdownSocketEvent tells the socket to forget a connection.
//
// lingerAck, when non-nil, is the acknowledgement to re-send if the peer keeps
// retransmitting its FIN after this connection has gone. Without it the socket
// answers that retransmission with a RESET, and a transfer that arrived whole
// ends as a connection reset for the peer. See UtpSocket.rememberLingerAck.
```
