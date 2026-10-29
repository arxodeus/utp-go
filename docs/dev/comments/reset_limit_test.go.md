# reset_limit_test.go

Every comment in `reset_limit_test.go` at e56c9aa, verbatim.

## Line 12

```
// A socket must not answer every packet addressed to a connection it does not
// have. Doing so is both an incompatibility with libutp and an amplification
// vector: a peer that keeps sending to a torn-down connection would draw one
// RESET per packet.
//
// libutp remembers what it has already answered, keyed on
// (connection id, address, seq nr), for RST_INFO_TIMEOUT, and stops answering
// entirely past RST_INFO_LIMIT stored entries (utp_internal.cpp:2907-2945).
```

## Line 25 — type recordingConn

```
// recordingConn is a Conn that feeds injected packets to a socket and records
// what the socket writes back.
```

## Line 86 — func (*recordingConn).stampsOfType

```
// stampsOfType returns when each packet of the given type was written.
```

## Line 103 — func dataPacketFor

```
// dataPacketFor builds an ST_DATA packet addressed to a connection id that no
// socket in the test has, which is what provokes a RESET.
```

## Line 119 — func TestResetIsSentOnceForARepeatedUnknownPacket

```
// The same unanswerable packet, over and over. libutp answers the first
	// and stays quiet for the rest.
```

## Line 127 — func TestResetIsSentOnceForARepeatedUnknownPacket

```
// give any extras a chance to appear
```

## Line 143 — func TestResetIsSentForEachDistinctUnknownPacket

```
// Distinct sequence numbers are distinct events, and each is answered --
	// suppression must not silence a genuinely new peer.
```

## Line 165 — func TestResetIsNeverSentInReplyToAReset

```
// Answering a reset with a reset lets two hosts trade them forever.
	// libutp handles ST_RESET in its own branch and returns without
	// responding (utp_internal.cpp:2850-2881).
```

## Line 178 — func TestResetStopsAtTheStoredLimit

```
// Past its stored-entry limit, a socket stops answering unknown packets
// entirely rather than letting the table grow without bound.
```

## Line 192 — func TestResetStopsAtTheStoredLimit

```
// Distinct connection ids so every one is a new entry.
```
