# duplex_test.go

Every comment in `duplex_test.go` at e56c9aa, verbatim.

## Line 18 — func TestFullDuplexEcho

```
// Reading and writing on the same stream at the same time, which is what
// BitTorrent does on every peer connection and what nothing in this
// repository tested until the anacrolix integration needed it.
//
// It failed: the send buffer retained the caller's slice, so an echo loop --
// read into a buffer, write it back, reuse the buffer -- transmitted whatever
// the buffer held by the time the connection got round to sending. Every
// unidirectional test passed throughout, because each writes one buffer once.
```

## Line 59 — func TestFullDuplexEcho

```
// Echo: read a chunk, write it back.
```
