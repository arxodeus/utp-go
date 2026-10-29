# netem/allocations_test.go

Every comment in `netem/allocations_test.go` at e56c9aa, verbatim.

## Line 10 — func TestAllocationsPerPacket

```
// TestAllocationsPerPacket bounds how much a bulk transfer allocates for each
// packet that crosses the link, data and acknowledgement alike.
//
// It was 13.2, everything in the process counted: the sender, the receiver,
// the emulated network and this harness. The library encoded every packet
// into a new slice, boxed every delay sample, gave each packet's
// retransmission timer three objects, copied the application's bytes twice
// on the way out and every datagram whole on the way in, and handed received
// data to the reader in a new buffer and result each time. Now about 5.3,
// nearly half of it the emulated network's own (profiled). In a process this
// small the collector's cost follows these: see KNOWN-LIMITATIONS.md, "An idle
// connection held a megabyte". The bound sits between the two.
```
