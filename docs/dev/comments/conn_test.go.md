# conn_test.go

Every comment in `conn_test.go` at e56c9aa, verbatim.

## Line 20 — func CreateTestConnection

```
// Create channels
```

## Line 24 — func CreateTestConnection

```
// Create peer address
```

## Line 31 — func CreateTestConnection

```
// Create connection ID
```

## Line 59 — func CreateTestConnection

```
// As newConnection has it. The fixture left it out, and nothing
		// noticed while every acknowledgement that retired all it covered
		// returned early from processAck, before the MTU search was
		// consulted. See TestAckRetiringEverythingRestartsTheDeadline.
```

## Line 78 — func TestOnSynInitiator

```
// A SYN at a connection we dialled is not ours to answer, and libutp
	// never lets one reach the connection ("connection already exists",
	// utp_internal.cpp:2957-2965). It used to reset it with
	// ErrSynFromAcceptor.
```

## Line 89 — func TestOnSynAcceptor

```
// Setup
```

## Line 105 — func TestOnSynAcceptorNonMatchingSyn

```
// Step 1: Setup the connection with expected syn
```

## Line 117 — func TestOnSynAcceptorNonMatchingSyn

```
// Step 2: a SYN with a different sequence number is another
	// connection's, one that drew the same ids. It is ignored, as libutp
	// ignores any SYN for a connection it has (utp_internal.cpp:2957-2965).
	// It used to reset this one with ErrInvalidSyn: the long soak saw a
	// peer's new connection kill an established one that way, 41 times in
	// 880,000 connections.
```

## Line 131 — func TestOnStateConnectingInitiator

```
// Setup
```

## Line 139 — func TestOnStateConnectingInitiator

```
// Test
```

## Line 143 — func TestOnStateConnectingInitiator

```
// Verify
```

## Line 150 — func TestOnPacketInvalidAckNum

```
// Setup connection
```

## Line 160 — func TestOnPacketInvalidAckNum

```
// Setup congestion control
```

## Line 164 — func TestOnPacketInvalidAckNum

```
// Send a packet
```

## Line 176 — func TestOnPacketInvalidAckNum

```
// Setup buffers and state
```

## Line 187 — func TestOnPacketInvalidAckNum

```
// An acknowledgement number past everything we have sent acknowledges
	// nothing -- and leaves the connection alone. libutp:
	//
	//	// this happens when we receive an old ack nr
	//	if (acks > conn->cur_window_packets) acks = 0;
	//	                                (utp_internal.cpp:1904-1907)
	//
	// This used to require the opposite: state connClosed and
	// errInvalidAckNum. That made one forged ST_STATE enough to end an
	// established transfer, and made an ordinary late or duplicated
	// acknowledgement fatal on a lossy path.
```

## Line 210 — func TestOnFinEstablished

```
// Setup
```

## Line 220 — func TestOnFinEstablished

```
// Create buffers and controllers
```

## Line 226 — func TestOnFinEstablished

```
// Set connected state
```

## Line 234 — func TestOnFinEstablished

```
// Test FIN
```

## Line 235 — func TestOnFinEstablished

```
// wrapping add in Go handles overflow automatically
```

## Line 239 — func TestOnFinEstablished

```
// Verify state
```

## Line 249 — func TestOnFinClosing

```
// Step 1: Setup initial connection
```

## Line 259 — func TestOnFinClosing

```
// Step 2: Setup buffers and controllers
```

## Line 265 — func TestOnFinClosing

```
// Step 3: Set local fin
```

## Line 278 — func TestOnFinClosing

```
// Step 4: Test remote fin
```

## Line 283 — func TestOnFinClosing

```
// Step 5: Verify state
```

## Line 296 — func TestOnFinClosingNonMatchingFin

```
// Step 1: Setup connection
```

## Line 306 — func TestOnFinClosingNonMatchingFin

```
// Step 2: Setup buffers
```

## Line 312 — func TestOnFinClosingNonMatchingFin

```
// Step 3: Set initial fin
```

## Line 325 — func TestOnFinClosingNonMatchingFin

```
// Step 4: Test with different fin
```

## Line 330 — func TestOnFinClosingNonMatchingFin

```
// Step 5: The end of the stream is still the first FIN's, and the
	// connection carries on. This used to assert a reset; libutp records
	// eof_pkt only for the first FIN (utp_internal.cpp:2316) and drops a
	// packet numbered past it without acknowledging it (:2381-2386).
	// TestConformanceSecondFinDoesNotMoveTheEnd compares the two directly.
```

## Line 342 — func TestOnResetNonClosed

```
// Setup
```

## Line 350 — func TestOnResetNonClosed

```
// A reset answering our SYN: the dial was refused.
```

## Line 357 — func TestOnResetNonClosed

```
// Once connected, a reset is a reset.
```

## Line 365 — func TestOnResetClosed

```
// Setup
```

## Line 373 — func TestOnResetClosed

```
// Set initial closed state
```

## Line 378 — func TestOnResetClosed

```
// Test reset
```

## Line 381 — func TestOnResetClosed

```
// Verify state remains unchanged
```
