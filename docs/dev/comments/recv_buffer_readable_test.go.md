# recv_buffer_readable_test.go

Every comment in `recv_buffer_readable_test.go` at e56c9aa, verbatim.

## Line 5 — func TestReceiveBufferReadable

```
// Readable is what lets a reader size its buffer to the data rather than to
// the largest packet the connection might carry, and it has to distinguish two
// states that IsEmpty cannot: a buffer with nothing in it, and a buffer holding
// only bytes that arrived out of order.
```

## Line 17 — func TestReceiveBufferReadable

```
// A packet past the gap. It is held, not readable: the bytes before it
	// have not arrived, and handing these over would deliver the stream out of
	// order.
```

## Line 30 — func TestReceiveBufferReadable

```
// Fill the gap. Both packets become readable at once.
```

## Line 38 — func TestReceiveBufferReadable

```
// And it tracks what is left after a partial read.
```
