# utpnet/sendnow_unix.go

Every comment in `utpnet/sendnow_unix.go` at e56c9aa, verbatim.

## Line 1 — package utpnet

```
//go:build unix
```

## Line 12 — func (*rawSender).sendNow

```
// sendNow makes one attempt to send p to to on c without waiting: the
// common case, which the kernel takes at once. It reports sent false only
// when the send would have had to wait -- a full send buffer -- or cannot be
// made this way, and then the caller hands the datagram to the writer, whose
// wait the write deadline can end.
//
// A Go socket is non-blocking underneath, so one sendto on its descriptor
// cannot wait. It is made under RawConn.Control, which only keeps the
// descriptor open, and not RawConn.Write: Write takes the descriptor's write
// lock first (internal/poll, FD.RawWrite), and waits for it while any other
// write on the port -- the uTP sender's, or the writer blocked on a full send
// buffer -- is under way, which is a wait no deadline can end either. The
// lock orders writes on a stream; a UDP datagram goes to the kernel whole, so
// concurrent sends need nothing of it. Release review of 224c363, finding 1.
```

## Line 42 — func (*rawSender).sendNow

```
// Interrupted, too, goes to the writer, which retries as net does.
```

## Line 52 — type sendAttempt

```
// sendAttempt is one sendNow's state, pooled with the function RawConn.Control
// calls bound to it once, so that an attempt allocates nothing: a closure or
// a sockaddr made per datagram cost four allocations a send.
```

## Line 72 — func (*sendAttempt).address

```
// address sets a.sa to to as the socket of the given family addresses it. A
// zoned address is left to the writer, which has net's interface lookup.
```

## Line 101 — type rawSender

```
// rawSender is a UDP socket as sendNow uses it, its RawConn made once.
```

## Line 108 — func newRawSender

```
// newRawSender is c ready for sendNow, or nil if its address family cannot
// be told: an IPv6 socket may be dual-stack, and then an IPv4 destination
// goes to it IPv4-mapped.
```
