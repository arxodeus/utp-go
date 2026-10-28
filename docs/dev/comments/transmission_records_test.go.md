# transmission_records_test.go

Every comment in `transmission_records_test.go` at e56c9aa, verbatim.

## Line 8 — func TestControllerKeepsRecordsOnlyForTheWindow

```
// The controller holds a record only for the packets the sender holds.
//
// Records used to be replaced only when a sequence number came round again,
// so a connection that had sent 65,536 packets held one for each, about 5 MB,
// for the rest of its life. libutp frees a packet's record when it is
// acknowledged (ack_packet, utp_internal.cpp:1397). Here 200,000 packets go
// out and are acknowledged a few at a time, three times round the sequence
// space; the controller must never hold more records than packets in flight.
```
