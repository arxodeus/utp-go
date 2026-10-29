# send_buffer.go

Every comment in `send_buffer.go` at e56c9aa, verbatim.

## Line 7 — type sendBuffer

```
// used is the length of everything in pending, the consumed front
	// included. Available and Pending are asked on every packet composed, and
	// summed the chunks each time: quadratic in a run of small writes.
```

## Line 25 — func (*sendBuffer).Pending

```
// Pending reports how many bytes of application data are buffered but not
// yet handed to the connection for transmission.
```

## Line 35 — func (*sendBuffer).Write

```
// Write copies data into the buffer and reports how much it took.
//
// The copy is not an optimisation to remove. Stream.Write returns as soon
// as the bytes are accepted here -- before they are transmitted -- and
// io.Writer's contract says "Implementations must not retain p": io.Copy,
// bufio.Writer and every echo loop reuse their buffer between writes.
// History: HISTORY.md, "sendBuffer.Write".
```

## Line 58 — func (*sendBuffer).Adopt

```
// Adopt takes data into the buffer without copying it, and reports how much
// it took. data must be the buffer's to keep: nothing else may change it.
//
// The write path's data already is. Stream.Write and WriteV each copy the
// caller's bytes once, into a slice of their own, before queueing them -- that
// copy is what keeps the caller's buffer free the moment Write returns (see
// Write above) -- and Write here then copied that private slice a second time.
// A 1 MB write was held twice until the second copy finished. With a
// thousand of them starting together (TestManyConcurrentTransfers) the
// process peaked at 4.53-4.84 GB, four runs; with one copy, 3.17-3.64 GB. libutp
// copies the application's bytes once, into its packets
// (utp_internal.cpp:1061).
```

## Line 80 — func (*sendBuffer).Read

```
// Read fills buf from the front of the buffer, across as many writes as it
// takes, and reports how much it copied.
//
// It used to stop at the end of the write it started in, so a packet never
// held bytes from two writes. libutp fills the last unsent packet before it
// starts another (write_outgoing_packet, utp_internal.cpp:1013-1023): a run of
// small writes queued together becomes full packets, and a bulk writer whose
// writes are not a multiple of the packet size does not leave a short packet
// at the end of every one.
```

## Line 104 — func (*sendBuffer).Take

```
// Take removes the next n bytes, or as many as there are, and returns them as
// a packet's payload.
//
// A payload that lies within one write is that write's own bytes, not a copy
// of them: the write was already copied in, by Write, and nothing changes it
// after. libutp copies the application's bytes once, into the packet
// (write_outgoing_packet, utp_internal.cpp:1061); copying again here made
// it twice, and one allocation for every packet sent. The slice is capped, so
// appending to it cannot reach the bytes after it.
//
// The write stays in memory until the last packet taken from it is
// acknowledged, rather than shrinking packet by packet. That holds at most a
// write's worth more than the window, and a write is at most the buffer.
```
