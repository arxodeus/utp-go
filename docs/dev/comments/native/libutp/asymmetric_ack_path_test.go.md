# native/libutp/asymmetric_ack_path_test.go

Every comment in `native/libutp/asymmetric_ack_path_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 17 — func TestAsymmetricAckPath

```
// TestAsymmetricAckPath measures what each receiver's acknowledgements cost a
// libutp sender: libutp sends to libutp, then to this library, over the same
// paced relay, and the time from Connect until the receiver holds every byte
// is compared.
//
// libutp acknowledges once per pass of its embedder's loop, which on a link,
// where packets arrive spaced, is once per packet: 1,750 acknowledgements a
// second for a 20 Mb/s flow, whatever the return path can carry. This
// receiver does the same until its acknowledgements queue on the way back,
// and then lets one cover several packets (connection.ackEvery). The gates
// hold that to its measured effect, 20 runs each:
//
//   - over a 160 kb/s return path, which carries 1,000 acknowledgements a
//     second, our receiver's median time is under asymmetricThinGain of
//     libutp's (measured 2.03 s against 2.99 s);
//   - over a 20 Mb/s return path it is no slower, beyond asymmetricSlack, and
//     sends no more acknowledgements than libutp's, beyond asymmetricAckShare
//     (measured 1.96 s and 2,744 against 1.96 s and 2,769);
//   - at 100 Mb/s and 1 ms both ways, the same two gates (measured 1.393 s
//     either way, 21 runs each, and 9,370 packets back against 10,207). The
//     relay's queue there is 2 MB, deeper than libutp's 100 ms delay target
//     at that rate (1.25 MB). It was 256 KB, and libutp's sender, filling
//     it near the end of the transfer, lost two packets and waited out a
//     timeout, about 2.72 s, in 7-14 runs of 21 for either receiver, so the
//     case could only gate on the best run.
//
// An acknowledgement for every read (ackEvery returning 1) fails the first:
// 3.01 s against libutp's 3.00 s.
```

## Line 53 — func TestAsymmetricAckPath

```
// gate the median against asymmetricThinGain
```

## Line 93 — func TestAsymmetricAckPath

```
// timing under the race detector; see raceEnabled
```

## Line 104 — func TestAsymmetricAckPath

```
// Gated as the times are, and only without the race detector.
				// A count, but of acknowledgements, one per read batch, and a
				// batch is however many datagrams the reader finds waiting:
				// it follows the reader's timing, which the race detector
				// changes for ours and not for libutp's, uninstrumented C.
				// Under it our receiver sent 0.65 of libutp's here and 1.054
				// and 1.057 on CI's runners, where plain runs, CI's included,
				// are at parity: 0.90-1.05 here. This used to hold it under
				// the race detector too, as a count independent of speed.
```

## Line 124 — const asymmetricRuns, asymmetricThinGain, asymmetricSlack, asymmetricAckShare...

```
// asymmetricAckRuns is how many runs a case gated on its count of
	// acknowledgements takes. The count is at parity with libutp's -- at
	// 100 Mb/s, eight medians of three runs each, four at 0f05aff and four
	// at 5a932e4, came to 0.960-1.045 of libutp's, about 1.00 on average --
	// but a median of three moves about 4% from run to run. Seven runs halve
	// the movement and keep the limit where it was. (The release runs at
	// 5a932e4 and e2804ff failed it at 1.054 and 1.057 under the race
	// detector, which is not what it measures: see the gate.)
```

## Line 135 — func libutpSendsTo

```
// libutpSendsTo times libutp sending payload to a receiver -- this library's
// when toGo, libutp's otherwise -- through a relay with data toward the
// receiver and back toward the sender, from Connect until the receiver holds
// every byte. It returns that time and how many datagrams the receiver
// offered to the return path.
```

## Line 196 — func libutpSendsTo

```
// The relay's toGo path faces the receiver, whichever it is; toLib carries
	// the receiver's acknowledgements back to the sender.
```

## Line 224 — func verifyPayload

```
// verifyPayload reports whether a receiver got exactly what was sent.
```
