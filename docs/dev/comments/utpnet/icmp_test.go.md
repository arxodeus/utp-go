# utpnet/icmp_test.go

Every comment in `utpnet/icmp_test.go` at e56c9aa, verbatim.

## Line 14 — func quotedHeader

```
// quotedHeader builds the twenty bytes a router quotes back: the front of a
// uTP datagram this side sent, carrying connId.
//
// Written out by hand rather than through the encoder, because this is the
// one place the library reads bytes it did not produce in this process -- the
// kernel hands them over from an ICMP message -- and a test that encoded them
// with the same code that decodes them would agree with itself whatever the
// layout was.
//
//	0        type<<4 | version
//	1        first extension
//	2-3      connection id
//	4-7      timestamp, microseconds
//	8-11     timestamp difference
//	12-15    window size
//	16-17    sequence number
//	18-19    acknowledgement number
```

## Line 34 — func quotedHeader

```
// ST_DATA, version 1
```

## Line 35 — func quotedHeader

```
// no extensions
```

## Line 45 — func TestSocketProcessICMPError

```
// The ICMP entry points reach the connection through this layer too. A
// BitTorrent client reading IP_RECVERR off its UDP socket has a net.Addr and a
// quoted payload, and this is where it hands them in.
//
// The connection ids are the socket's own, so the test asks the socket what
// they are rather than guessing: any live connection to the peer will do,
// which is what a real caller has as well.
```

## Line 76 — func TestSocketProcessICMPError

```
// The dialled connection's send id is what its data packets carry.
```

## Line 109 — func TestSocketProcessICMPFragmentation

```
// The fragmentation entry point, and what it refuses. A nil address must not
// be turned into a peer that matches something.
```

## Line 152 — func TestSocketProcessICMPFragmentation

```
// The connection is still usable: a fragmentation report changes the size
	// of what is sent, not whether anything is sent.
```
