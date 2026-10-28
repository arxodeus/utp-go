# reset_ends_connection_test.go

Every comment in `reset_ends_connection_test.go` at e56c9aa, verbatim.

## Line 10 — func TestResetAnsweringOurAckEndsTheConnection

```
// A RESET ends the connection whatever acknowledgement number it carries, and
// a reader blocked on it is told.
//
// A peer's socket answers a packet for a connection it no longer has with a
// RESET acknowledging that packet's sequence number. When the packet was one
// of our acknowledgements or keep-alives, that is our next, unsent number,
// which the acknowledgement check took for acknowledging a packet never sent:
// the RESET was dropped, and the connection, its peer gone, ran on with its
// reader waiting. libutp acts on a RESET before checking anything else
// (utp_internal.cpp:2850-2873). The long soak left server connections
// running this way for clients that had given up. Here the RESET answers our
// SYN-ACK, a STATE; the reader must return at once.
```
