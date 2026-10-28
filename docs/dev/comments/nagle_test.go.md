# nagle_test.go

Every comment in `nagle_test.go` at e56c9aa, verbatim.

## Line 9 — func nagleTestConn

```
// nagleTestConn is a connected connection with room in both windows for far
// more than these tests send, so that only the Nagle rule can hold a packet.
```

## Line 22 — func TestNagleHoldsAShortPacketBehindAnother

```
// libutp's Nagle rule (flush_packets, utp_internal.cpp:974-982), with both
// windows open: a short packet waits while another is unacknowledged, and
// what is written next joins it until it is a full packet.
```

## Line 34 — func TestNagleHoldsAShortPacketBehindAnother

```
// The rest of a packet's worth joins the held bytes and fills it.
```

## Line 40 — func TestNoDelaySendsAShortPacketAtOnce

```
// The control: the same short write with NoDelay leaves at once.
```

## Line 51 — func TestNagleReleasedWhenTheWriteSideCloses

```
// Closing the write side releases a held packet: nothing more will join it.
// libutp queues its FIN behind the held packet, which is then no longer the
// last and goes out (utp_close, utp_internal.cpp:3358-3380; flush_packets).
```
