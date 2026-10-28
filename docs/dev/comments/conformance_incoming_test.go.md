# conformance_incoming_test.go

Every comment in `conformance_incoming_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 7

```
// Cases from reading utp_process_incoming line by line (LIBUTP-AUDIT.md).
```

## Line 9 — func TestConformanceDuplicateOutOfOrderData

```
// An out-of-order packet that has already arrived is discarded with no
// acknowledgement: libutp returns before schedule_ack (utp_internal.cpp:
// 2425-2431).
```

## Line 36 — func establishedSteps

```
// establishedSteps completes libutp's side of the connection. Its acceptor
// stays in CS_SYN_RECV until the first ST_DATA (utp_internal.cpp:2157) and
// drops anything else that arrives before it (:2311), which is a recorded
// deviation ("Completing an incoming connection") and not what these cases
// are about.
```

## Line 53 — func TestConformanceDataPastTheFinIsDropped

```
// Data numbered past the peer's FIN, arriving out of order, is dropped and the
// connection carries on: `if (conn->got_fin && pk_seq_nr > conn->eof_pkt)
// return 0;` (utp_internal.cpp:2381-2386).
```

## Line 79 — func TestConformanceSecondFinDoesNotMoveTheEnd

```
// A second FIN with another sequence number does not move the end of the
// stream, and does not end the connection: libutp records eof_pkt only
// `if (pk_flags == ST_FIN && !conn->got_fin)` (utp_internal.cpp:2316) and
// otherwise treats the packet like data.
```

## Line 109 — func TestConformanceDuplicateSynBeforeData

```
// A SYN for a connection that already exists draws nothing from libutp: the
// lookup of (address, id + 1) finds the socket and utp_process_udp returns
// (utp_internal.cpp:2955-2958). This library sends its SYN-ACK again, which is
// a recorded deviation ("A retransmitted SYN is answered"): a libutp acceptor
// whose SYN-ACK was lost never answers the initiator's retries.
```

## Line 127 — func withExtensionBits

```
// withExtensionBits returns the encoded packet with an "extension bits"
// extension (type 2) of n bytes ahead of its payload.
```

## Line 141 — func TestConformanceExtensionBitsMustBeEightBytes

```
// An "extension bits" extension must be 8 bytes long, or libutp drops the
// packet (utp_internal.cpp:1850-1856).
```
