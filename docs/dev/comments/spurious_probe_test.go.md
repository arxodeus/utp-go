# spurious_probe_test.go

Every comment in `spurious_probe_test.go` at e56c9aa, verbatim.

## Line 10 — func TestSpuriousLossProbeResendsOnlyItself

```
// A loss probe that fired because the round trip jumped, not because anything
// was lost, resends the probe and nothing else.
//
// A queue filling in slow start can take the round trip from 10 ms to 33 ms
// faster than the smoothed estimate the probe's timer runs on. The probe then
// resends the oldest packet while it, and everything behind it, is still in
// the queue, and the first acknowledgement after the probe is the original's.
// Taken for the probe's answer, it declared every packet sent before the probe
// lost: 86-101 needless retransmissions on a lossless 100 Mb/s link over the
// emulated network, about one run in three. Here the scripted peer reports
// its delay as a real one does, so its acknowledgements say which packet drew
// them, and one round's are held four round trips by a queue.
```

## Line 24 — func TestSpuriousLossProbeResendsOnlyItself

```
// The same with a peer that reports no delay, so no timestamps to go on,
	// and the acknowledgements held until the probe has gone and then let
	// through together, as when this process stalls: the original's arrives
	// 5 ms after the probe, under the path's 50 ms minimum round trip, which
	// is RFC 8985's test and the one left when there is no delay to read.
```

## Line 78 — func spuriousProbe

```
// The peer: what it holds, and the send timestamp of the last packet it
	// received. Its acknowledgement carries its own clock, half a round trip
	// before the acknowledgement arrives here, and the delay it measured for
	// that packet -- so their difference is that packet's send timestamp, as
	// with a real peer.
```

## Line 85 — func spuriousProbe

```
// The stalled case's peer stops reporting a delay for the spiked round
	// only: LEDBAT leaves a window where it is without delay samples, so the
	// window could not grow first.
```

## Line 124 — func spuriousProbe

```
// Grow the window, every packet acknowledged one round trip after it left.
```

## Line 148 — func spuriousProbe

```
// This round's packets all arrive, and each is acknowledged when it
	// does, but a queue holds them for four round trips. Whatever is sent
	// meanwhile -- the probe among it -- comes after them.
```
