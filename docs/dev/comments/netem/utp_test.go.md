# netem/utp_test.go

Every comment in `netem/utp_test.go` at e56c9aa, verbatim.

## Line 17

```
// The harness has to actually carry uTP, and the connection-level metrics
// have to record something. Without this, the M5 gates would be measuring an
// emulator nobody had run a real flow over.
```

## Line 61 — func TestCongestionWindowRespondsOverEmulatedLink

```
// The window has to move. A congestion controller whose window never changes
// is not controlling anything -- which is exactly the failure this fork
// already found once, and the reason the harness records cwnd at all.
```

## Line 101 — func TestCongestionWindowRespondsOverEmulatedLink

```
// The link has 20ms one-way delay each way, so a sane RTT estimate must
	// be at least that. This is what catches an RTT estimator wired to the
	// wrong clock.
```

## Line 109 — func TestUtpSurvivesLossOverEmulatedLink

```
// Loss must be survivable, and must show up as retransmissions.
```

## Line 153 — func TestTwoUtpFlowsShareBottleneck

```
// Two flows over one bottleneck, and the capacity split reported. This is the
// mechanism the M5 fairness gate needs; here it only has to work, not to be
// fair -- judging the split is M5's job.
```

## Line 207 — func TestTwoUtpFlowsShareBottleneck

```
// The harness gate is only that both flows complete and the split is
	// reportable. Whether the split is *fair* is an M5 question about the
	// congestion controller, not about this emulator.
```

## Line 215 — func TestLinkConfigCanChangeMidFlight

```
// Conditions can change mid-flight, which soak and recovery tests need.
```

## Line 255 — func TestSpuriousRetransmitsOnLosslessLink

```
// TestSpuriousRetransmitsOnLosslessLink measures retransmissions on a link
// that drops nothing. The correct answer is zero: with no loss and no
// reordering, nothing should ever need resending.
//
// It was not zero. On an 8 Mbps / 40 ms path this implementation retransmitted
// about 6% of packets, because each connection owned a retransmission timer
// wheel with a one-second resolution (interval = InitialTimeout/4) which
// cannot represent the 500 ms RTO -- a timer set for "500 ms" landed on the
// next tick, uniformly 0-1000 ms away, so packets were declared lost long
// before their ack could arrive.
//
// The wheel is now shared across the socket at 25 ms resolution, which buys
// the accuracy without a ticker per connection. Measured: 0.00%.
```

## Line 303 — func TestSpuriousRetransmitsOnLosslessLink

```
// A lossless link should need no retransmissions at all. The small
	// allowance covers a genuinely late ack under scheduler noise, not the
	// systematic mistiming this test was written to catch.
```

## Line 311 — func TestConnectionGivesUpWhenPeerGoesSilent

```
// TestConnectionGivesUpWhenPeerGoesSilent covers libutp's give-up rule: a
// connection dies after a few consecutive retransmission timeouts with
// nothing acked, rather than lingering until the idle timer.
//
// Without it a connection has no death condition of its own. The idle timer
// is reset by *any* inbound packet, so a half-broken peer that keeps sending
// anything at all holds the connection open indefinitely while data goes
// unacked -- and for a BitTorrent client with many peers, that is a lot of
// dead connections held open.
```

## Line 333 — func TestConnectionGivesUpWhenPeerGoesSilent

```
// Shorten the RTO so the four retransmissions do not take ten seconds.
	// An idle timeout of 60 s, which was the default, is set as well: the
	// point is that the give-up rule fires long before it. (By default there
	// is none, as in libutp, and then the give-up rule is all there is.)
```

## Line 365 — func TestConnectionGivesUpWhenPeerGoesSilent

```
// Blackhole the path in both directions: the peer is now unreachable but
	// the connection is fully established.
```

## Line 375 — func TestConnectionGivesUpWhenPeerGoesSilent

```
// Write more than the send buffer holds, so the call blocks waiting for
	// window rather than returning as soon as the buffer accepts it. That is
	// the case a caller actually notices: a write that can never complete
	// must fail, not hang until the idle timer.
```

## Line 399 — func TestConnectionGivesUpWhenPeerGoesSilent

```
// Four retransmissions with the configured backoff is a couple of
		// seconds at most; anything near the idle timeout means the give-up
		// rule is not what ended it.
```
