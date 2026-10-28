# History

Why the code is the way it is, at length. Each section is a comment that
stood in the code, kept whole: the defect it guarded against, how that was
found, what was measured, and what was tried before. The code now says what
it does and why in a few lines, and points here for the rest.

Sections are named for the declaration they explain.
Line numbers, measurements and test names are as they were when the comment
was written; [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) and
[DEVIATIONS.md](DEVIATIONS.md) are where the current state of each is kept.

## NoteHandoff

`clock.go`

NoteHandoff reports that something is being queued for another
participant, and TakeHandoff that it has been picked up. Called by the
sender before the send and by the receiver once it holds the item, so
that every outstanding handoff is accounted for by exactly one of them.

Without it "every participant is parked" is not the same as "nothing is
in flight". A channel send returns long before the receiver runs, so a
participant can hand a packet to another and park, leaving the system
momentarily quiet by every observable measure while work is still
outstanding. Time would then move before the receiver had reacted.

Measured before it existed: the retransmission wheel delivers a timeout
and parks, the clock judged the system quiet, and the connection's
re-arm landed a tick or two later than it should -- giving a
retransmission at 9.05s under one scheduler and 9.075s under another
for the same virtual deadline.

## defaultMaxPacketSizeBytes

`congestion.go`

defaultMaxPacketSizeBytes is the largest datagram this library will
ever try -- the ceiling of the path-MTU search, not a size it sends
straight away.

libutp derives its ceiling from the interface MTU (`get_udp_mtu`,
utp_internal.cpp:1316) and searches downward from there. With no
embedder callback its default for IPv4 is UDP_IPV4_MTU, 1402: a
1500-byte Ethernet MTU less the IPv4 and UDP headers and an allowance
for GRE, PPPoE, MPPE and 36 bytes of "fudge" (utp_utils.cpp:211-228).
This used to be 1400, two bytes short of it for no reason anyone
recorded. IPv6 peers get libutp's 1232 (path_mtu.go).

This was a flat 1024 before path-MTU discovery existed, because without
discovery the only safe fixed size is a small one. With discovery the
search starts here, as libutp's does, and comes down when a probe of
this size is lost. See mtu.go.

## forgetTransmission

`congestion.go`

forgetTransmission drops the record of an acknowledged packet that the
sender no longer holds, keeping it for the next packet sent.

Records were only ever replaced, when the sequence number came round again,
so the map grew to one per sequence number: 65,536 records, 5.5 MB
measured, on every connection that had sent that many packets. libutp's
records are its outgoing packets, freed when acknowledged (the free(pkt) in
ack_packet, utp_internal.cpp:1397), so it holds only the window.

A record still unacknowledged is kept: the sender drops only packets it has
seen acknowledged, so this cannot happen from it, and a caller driving the
controller directly keeps what it has not finished with.

## onLedbatPPLoss

`congestion.go`

onLedbatPPLoss is LEDBAT++'s response to a lost packet. Called with the
lock held; decay is whether the 100ms rate limit classic LEDBAT applies
would allow a halving now.

Two rules beyond the rate limit, both about which window a loss belongs
to, and both found by logging two LEDBAT++ flows that split a shallow
bottleneck unevenly (KNOWN-LIMITATIONS.md). On that bottleneck slowdowns
and queue overflows coincide, and a flow that was charged for one overflow
twice, or charged it against the wrong window, came out of the slowdown
with a fraction of the window the other flow did.

  - A packet sent before the window was last cut belongs to the congestion
    event that cut it, and is not charged again. That is TCP NewReno's
    rule (RFC 6582's "recover"). The 100ms rate limit alone let one
    overflow halve the window twice when its losses were detected more
    than 100ms apart, as they are when a slowdown falls between them.
  - A packet sent before a slowdown was sent from the window the slowdown
    saved, not from the two packets of the freeze or the partial window of
    the ramp. Its loss halves the saved window, which is where the ramp is
    going; the window the ramp has reached is left alone. Halving the
    current window instead, and taking it as ssthresh, ended the ramp at a
    fraction of the window the connection had been using. RFC 5681 halves
    the data in flight, and the data in flight is from the saved window.

The draft does not cover a loss during a slowdown.

## ApplyAck: the window at sending

`congestion.go`

Deviation: each acknowledged packet counts against the window it was
sent in, not the window as it stands now, when the caller knows it
(credit > 0). libutp's factor is meant to share one round trip's
growth among that round trip's acknowledgements -- a full window of
them adding MAX_CWND_INCREASE_BYTES_PER_RTT -- but each update grows
the window the next one divides by, so the more acknowledgements a
round trip is split into, the less it grows: by about G^2/2W, a
quarter of G at a 6 KB window. Measured over the emulated network,
classic LEDBAT on a 100 ms, 20 Mb/s path took 8.20 s for 2 MB when
each acknowledgement covered one packet and 8.04 s when it covered
two, the window a packet behind for the whole transfer; reckoned
against the window at sending, 7.80 s either way. DEVIATIONS.md, "The
window grows by a round trip's worth per round trip".

The larger of the two, though, not the send-window figure alone. After
a loss halves the window, libutp divides the next round trip's
acknowledgements -- for packets sent from the old window -- by the new
one, and that round trip grows about twice as fast. Reckoned against
the window they were sent in, it lost that, and classic LEDBAT at 5%
loss ran 9% slower. Taking the larger, the window never grows slower
than libutp's would; above target it shrinks no slower either, the
factor scaling the decrease the same way.

## applicationLimited

`congestion.go`

applicationLimited reports whether the sender has gone more than a second
without filling its window: libutp's `current_ms - last_maxed_out_window >
1000` (utp_internal.cpp:1681).

A sender that has never filled its window counts as application-limited, as
it does in libutp. libutp initialises last_maxed_out_window to 0 (:2603) and
reads current_ms from a clock that counts from boot, not from the process or
the connection (CLOCK_MONOTONIC on POSIX, utp_utils.cpp:158-175), so unless
the host booted within the last second the difference is already past 1000
and the gain is zeroed until the window first fills. This used to be the
other way round here, on the mistaken reading that libutp's counter starts
near zero; TestConformanceLedbatRules replays libutp's own trace and caught
it. A bulk sender is unaffected, because its first write fills the window
and records the time before any acknowledgement arrives. What changes is a
sender that never fills its window: it now grows only by the slow-start
step, and not at all after slow start, where it used to take the LEDBAT
gain as well.

## peerDelayHist

`congestion.go`

peerDelayHist tracks the base delay of packets arriving *from* the peer:
libutp's `their_hist`. Its only purpose is noticing that base fall, which
is how clock drift becomes visible.

It is deliberately not a delayAccumulator, for one reason: these samples
must stay in wrapping 32-bit microsecond arithmetic.

A clock drifting slow makes the measured inbound delay *shrink*, and it
keeps shrinking -- at 100ppm it loses 10ms every 100 seconds, so on an
ordinary path it passes zero within minutes and the subtraction wraps. In
Duration terms that wrapped value is about 4295 seconds, which the
connection then caps to one second as an unusable clock reading, and the
base stops moving: precisely when the drift has grown large enough to
matter, the evidence for it disappears.

That was measured before this type existed. The correction reached about
10ms and then plateaued in every run, whatever the drift rate -- which is
how long it took for the accumulated skew to pass the inbound one-way
delay of the emulated path.

libutp has no such problem: `their_delay` is a raw wrapping uint32
throughout and every comparison in DelayHist goes through
wrapping_compare_less. This does the same.

## baseDelayHist

`congestion.go`

baseDelayHist is libutp's DelayHist for this sender's own delay samples,
our_hist (utp_internal.cpp:260-381): the base delay is the least of
thirteen buckets, the newest taking each sample that is lower than it,
rotated onto a fresh bucket when a sample arrives a bucket's length after
the last rotation. With the default window a bucket is a minute, so the base
remembers twelve to thirteen minutes, as libutp's does, and an idle
connection's history does not age, since only a sample rotates it.

It was a sliding minimum over two minutes. That held every sample that
could still become the least, and under clock drift every sample can: the
delays climb at the drift rate. At 1000 ppm and a thousand acknowledgements
a second it held 120,001 samples, 3.75 MB, with no jitter, and 69 KB with
5 ms of it, per connection; over thirteen minutes, six and a half times
that. Thirteen buckets are 104 bytes.

Two minutes was chosen because drift inflates the base by the drift rate
times the window, before this library had libutp's correction for it
(OnPeerDelay). With the correction, phantom queueing delay from drift is
0.3-0.5% of what it was (netem.TestClockSkewCorrectionCancelsThePhantomQueue),
and libutp's length is libutp's behaviour: a queue that stands for longer
than two minutes is no longer taken for the empty path.

## OnPeerDelay

`congestion.go`

OnPeerDelay reports the one-way delay measured on a packet arriving from
the peer, and corrects this sender's delay signal for clock drift.

libutp:

    uint32 prev_delay_base = conn->their_hist.delay_base;
    if (their_delay != 0) conn->their_hist.add_sample(their_delay, conn->ctx->current_ms);

    // if their new delay base is less than their previous one
    // we should shift our delay base in the other direction in order
    // to take the clock skew into account
    if (prev_delay_base != 0 &&
        wrapping_compare_less(conn->their_hist.delay_base, prev_delay_base, TIMESTAMP_MASK)) {
        // never adjust more than 10 milliseconds
        if (prev_delay_base - conn->their_hist.delay_base <= 10000)
            conn->our_hist.shift(prev_delay_base - conn->their_hist.delay_base);
    }
                                            (utp_internal.cpp:2002-2014)

# Why a fall in the other direction means drift in this one

A one-way delay is a difference between two clocks, so it carries their
relative rate error. If this end's clock runs slow, the time it stamps into
its own packets falls further behind, and the peer's measurement of this
sender's path -- which is what LEDBAT here runs on -- grows without bound.
The same slow clock makes packets *from* the peer appear to arrive sooner,
so the delay measured in that direction shrinks by exactly as much.

So a falling base delay in the peer's direction is the visible half of a
bias that is inflating the invisible half. Raising this sender's base by
the same amount cancels it.

# What it is worth here

Measured, before it existed: the phantom queueing delay settles at the
delay window multiplied by the drift rate, and what it costs is fairness
rather than throughput -- a drifted flow took 32-33% of a shared bottleneck
against an undrifted flow's 67-68%, and 50-51% with the correction. See KNOWN-LIMITATIONS.md, and
netem.TestClockDriftYieldsShareAtASharedBottleneck.

## ledbatPPWindowDeltaPerRTT

`congestion_ledbatpp.go`

ledbatPPWindowDeltaPerRTT is the per-round-trip change in the congestion
window, in packets, for a connection in congestion avoidance. draft §4.3,
which gives both branches:

    delay below target:  W += GAIN
    delay above target:  W += max( (GAIN - Constant * W * (delay/target - 1)), -W/2) )

Additive increase and multiplicative decrease. That pairing is the point
of the section: RFC 6817's increase, GAIN * (1 - delay/target), shrinks to
nothing as the delay nears the target, and so does the decrease, so near
the target neither acts and whatever split two flows arrived at persists --
the "persistent unfairness" of proportional feedback (§3.2). A constant
increase keeps pushing until the decrease, proportional to each flow's
window, takes more from the larger.

This used to use RFC 6817's increase below the target, on the reading that
the draft gave only the decrease formula and left the increase ambiguous.
It does not: "In LEDBAT++, with multiplicative decrease, the per RTT window
when delay is less than target is: W += GAIN". With the proportional
increase, two LEDBAT++ flows that left slow start at different windows kept
most of the difference. See KNOWN-LIMITATIONS.md.

A free function, so the shape of the response curve can be tested directly
at the interesting points rather than inferred from a transfer.

## ledbatPPRTTDelay

`congestion_ledbatpp.go`

ledbatPPRTTDelay is LEDBAT++'s base and current round trip, whose
difference is its queueing delay. draft §4.5: "LEDBAT++ uses Round Trip
Time measurements instead of one way delay", filtered by "the minimum of
the 4 most recent delay samples"; the base is the lowest round trip within
the delay window, as the one-way base is for classic LEDBAT.

This used to be the one-way delay classic LEDBAT uses. A round trip is
timed entirely on this end's clock, so it has no clock skew to correct and
no peer to trust, though it does include queueing on the return path and
any delay in acknowledging. Measured against the one-way delay, fifteen
runs each: deferring to a loss-based flow, LEDBAT++'s share while both
sent went from 27-30% in five runs and 50-73% in ten to 23-33% in fourteen
and 55% in one, and the bottleneck queue's median from 21 to 13 ms. It was
tried for the latecomer problem, and did not fix it (KNOWN-LIMITATIONS.md).

Both are zero until the first sample.

## OnAck: LEDBAT++ slow-start exit

`congestion_ledbatpp.go`

draft §4.1: exit on a queueing delay over 3/4 of the target -- but
"Exit slow start on excessive delay SHOULD be applied only during
the initial slow start." The ramp out of a slowdown runs "until the
congestion window reaches SSTHRESH" (§4.4) and nothing else.

It used to exit the ramp on delay as well, and that is what split a
link unevenly between two LEDBAT++ flows. Their slowdowns
synchronise, as §4.4 intends; the one that ramps first refills the
queue, or the queue has not finished draining from before the
slowdown, and the other reads a delay over the threshold and leaves
its ramp at two packets. It then climbs back at GAIN packets a
round trip, and since the slowdown it measured was short, the next
comes nine short durations later and knocks it back again.
Measured on two flows through an 8Mb/s bottleneck with a 2MB
queue: the squeezed flow took 17.8-33.9% of the link while both
ran. See KNOWN-LIMITATIONS.md.

## OnAck: the ramp out of a slowdown

`congestion_ledbatpp.go`

The initial ramp is gain-scaled, draft §4.1: increase "by that
number multiplied by the dynamic GAIN value". The ramp back out of
a slowdown is not.

§4.4 says only "ramp up the congestion window according to the
slow start algorithm, until the congestion window reaches
SSTHRESH", and separately that a slowdown should cost "not more
than a 10% drop in the utilization of the bottleneck". Those two
statements only hold together if the ramp doubles per round trip.
Gain-scaled on a 20 ms path the gain is 1/6, so the window grows by
a seventh per round trip and climbing back from two packets to
fifty takes about twenty-one of them -- measured, that turned a
1.3 s transfer into 2.8 s and cost more than half the throughput.

There is also no reason for the ramp to be cautious: it is
returning to a window this connection was already using a moment
ago, and it stops at exactly that window.

## rtoDeadline

`conn.go`

rtoDeadline is when this connection's retransmission timeout expires.

libutp has exactly one of these per connection (`rto_timeout`,
utp_internal.cpp:494): armed when the first packet enters an empty
window (:994-998), reset on every acknowledgement of new data
(:1388-1389), and compared against the clock before a timeout is
declared (:1147-1148).

This connection arms one timer per outstanding packet, so without a
deadline of its own a single RTO expiry produced several timeout
events, and the wheel's resolution let a callback arrive before the
timeout had actually elapsed. Measured against libutp: our backoff
doubled once every *two* retransmissions instead of every one, and the
first retransmission fired at roughly 0.6 of the RTO. See
conformance_timing_test.go.

## newConnection: no delay from the SYN

`conn.go`

No delay is measured from the SYN, and the SYN-ACK therefore
echoes a timestamp difference of zero.

libutp processes an incoming SYN with `utp_process_incoming(conn,
packet, len, /*syn=*/true)`, and that call returns at

    if (syn) {
        return 0;
    }

before ever reaching `conn->reply_micro = their_delay`
(utp_internal.cpp:2002). So reply_micro keeps the zero it was
initialised with (:2617) until a non-SYN packet arrives, and zero
is a defined signal rather than a measurement: "if the actual delay
is 0, it means the other end hasn't received a sample from us yet".

This used to measure the SYN and echo it, which gave the initiator
a delay sample a round trip earlier than the reference does -- and
one derived from a single packet, carrying whatever offset stands
between two clocks that have exchanged nothing yet. The corpus
could not see it: the timestamp fields were excluded from
comparison until this connection's clock became injectable, which
is what that exclusion was hiding.

## ackEvery

`conn_ack.go`

ackEvery is how many data packets an acknowledgement may wait to cover.

libutp acknowledges once per pass of its embedder's loop: whatever arrived
while it was busy shares one acknowledgement, and a packet that arrives
alone gets its own. On a link, where packets arrive spaced, that is one per
packet -- measured over real sockets with the relay pacing each datagram to
its time, 0.99 per data packet at 10 Mb/s and 0.98 at 20 Mb/s -- and only
at 100 Mb/s, where packets come 110 us apart, does its loop gather a few
(0.58-0.62). So libutp sends a 20 Mb/s flow's acknowledgements at 1,750 a
second whatever the return path can carry: over a 160 kb/s return path,
which carries 1,000 a second, it took 2.97 s to receive 4 MB where 2.0 s
was the data path's limit, and over 64 kb/s 7.3 s.

This receiver acknowledges as libutp does until its acknowledgements are
seen to queue on the way back, and then lets one wait for more packets, by
two terms, the larger winning:

  - The return path: the peer reports on every packet how long ours take to
    reach it, and for a connection that only receives, ours are its
    acknowledgements. Each ackQueueStep of filtered queueing delay on that
    path (libutp's our_hist.get_value()) adds one packet to the run.
  - The rate, for ackRateMemory after that delay last reached ackRateGate:
    while packets arrive closer together than ackRatePeriod, round(1 ms /
    gap) of them share one, at most maxAckRateRun. The gap is the smaller
    of the latest and a smoothed one, so a burst after a pause is coalesced
    from its second packet. Without it the queue the first term answers to
    stands: 2.13 s over 160 kb/s and 3.38 s over 64 kb/s.

Measured against libutp receiving, a libutp sender, over real sockets
(native/libutp TestAsymmetricAckPath), 20 runs each: 2.03 s against 2.99 s
over 160 kb/s and 2.43 s against 7.33 s over 64 kb/s; over 320 kb/s, 20
Mb/s and 100 Mb/s the same times and as many acknowledgements as libutp's.
The rate term used to apply on every path, and cost the wait it imposes: at
100 Mb/s the median acknowledgement left 240 us after its packet arrived,
against libutp's 30 us and 50 us now (TestAckTurnaround), for no gain in
throughput there. DEVIATIONS.md, "Acknowledgements: one per read, fewer
when they would crowd the way back".

Never while a gap is open: the selective ack is how the peer learns of a
loss, and holding it back would delay recovery.

And only when the queue is this receiver's own. Coalescing helps when the
way back is clogged with its acknowledgements; then sending fewer drains
it. When the queue is other traffic's -- an uplink shared with someone's
upload -- fewer acknowledgements change nothing there, and cost the
sender's yielding, libutp's sender as much as ours. Measured with a
loss-based flow sharing one bottleneck both ways, where this receiver
coalesced to a third of libutp's acknowledgements: the competitor kept
64.0% of its rate against 67.5% with libutp's receiver, and 67.5% with
this one acknowledging as libutp does (30 runs each). So it coalesces only
after the queue has been seen climbing as only its own acknowledgements
make it climb (ackPathSelfQueued), and otherwise acknowledges every read,
as libutp does.

## flushAck: one per read

`conn_ack.go`

One acknowledgement per socket read, as libutp's embedder sends it: the
datagrams already waiting in the kernel are all processed, and then
utp_issue_deferred_acks runs once (utp.h:512-517). A read that is still
being handed out holds the acknowledgement; the reader ends the batch
(endBatch) once it has dispatched the whole read, and the
acknowledgement then covers everything the read brought.

This used to flush at the end of every pass of this loop, and the batch
boundary was wherever the goroutine hand-offs between the socket and
here happened to fall. With batches alone, and ackEvery at one, a
libutp sender at 100 Mb/s got 0.57-0.68 acknowledgements per data
packet from this receiver and 0.58-0.62 from libutp's.

## invalidAckNum

`conn_ack.go`

invalidAckNum reports whether a packet acknowledges something this
connection never sent, or something so old it cannot be meaningful.

libutp (utp_internal.cpp:1794-1807), which is unusually explicit about why:

    // ignore packets whose ack_nr is invalid. This would imply a spoofed
    // address or a malicious attempt to attach the uTP implementation.
    // acking a packet that hasn't been sent yet!

    const uint16 curr_window = max<uint16>(
        conn->cur_window_packets + ACK_NR_ALLOWED_WINDOW, ACK_NR_ALLOWED_WINDOW);
    if ((pk_flags != ST_SYN || conn->state != CS_SYN_RECV) &&
        (wrapping_compare_less(conn->seq_nr - 1, pk_ack_nr, ACK_NR_MASK)
         || wrapping_compare_less(pk_ack_nr, conn->seq_nr - 1 - curr_window, ACK_NR_MASK)))
        return 0;

The acceptable range is a short window ending at the last sequence number
actually sent. Anything above it acknowledges a packet that does not exist
yet; anything below is too stale to act on.

This fork had no such rule. CONFORMANCE.md previously recorded that we
"already match" here, on the strength of two corpus cases that happened to
produce the same silence for other reasons -- the behaviour was incidental,
not implemented. FuzzDifferentialResponder showed the difference with a
single ST_DATA whose ack_nr named a packet we had never sent: libutp
dropped it without a word, we answered it.

## processAck: an acknowledgement outside the window

`conn_ack.go`

An acknowledgement number outside what this connection has
outstanding acknowledges nothing. It does not end the
connection.

libutp computes how many packets an acknowledgement covers and
then throws the answer away when it exceeds the window:

    int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
    // this happens when we receive an old ack nr
    if (acks > conn->cur_window_packets) acks = 0;
                                    (utp_internal.cpp:1904-1907)

This used to call reset, which is the divergence
CONFORMANCE.md records as one the packet-injection corpus
cannot see: both sides answer such a packet with silence, so
the transcripts match while one connection is dead and the
other is not.

Two reasons it had to go. It is an attack surface -- a single
forged ST_STATE from anyone who can guess a connection id ends
an established transfer -- and it is a live defect: a delayed
or duplicated acknowledgement arriving after the window has
moved on is an ordinary event on a lossy path, and it killed
connections that should have carried on. It is what made
integrated.TestCloseSucceedsIfOnlyFinAckDropped fail about one
run in eight, reporting "invalid ack number" where the test
expects the idle timeout.

The selective-ack extension on such a packet is dropped with
it, where libutp would still apply it. That is deliberate and
narrow: our extension is applied relative to this same
acknowledgement number, so honouring it would mean indexing the
loss-detection machinery off a figure just established to be
outside the window.

## processAck: restarting the timeout

`conn_ack.go`

Restart the retransmission timeout, but only when this ack actually
retired something.

libutp resets rto_timeout from inside its RTT-sample path
(utp_internal.cpp:1388-1389), which runs once per newly acknowledged
packet -- not once per ack received. Resetting on every ack looks
equivalent and is not: duplicate acks are how a peer reports a hole,
so on a lossy path they arrive in a stream, and each one would push the
deadline further out. The RTO would then never fire, and a packet that
fast retransmit could not recover -- it resends each packet once, and
at most four per ack -- would never be retransmitted at all.

Measured: with the reset unconditional, the 5%-loss and broadband
profiles in the netem benchmark suite stopped completing, the receiver
timing out after 76 seconds on a transfer that takes one.

One deliberate difference: libutp's ack_packet also runs for a packet
acknowledged selectively (:1529), so a selective ack moves its deadline
too. Here only the cumulative acknowledgement does. Adopting libutp's
rule was measured and was slower -- see DEVIATIONS.md, "A selective
ack does not restart the retransmission timeout".

Not when this is the loss probe's acknowledgement and it has shown
other packets lost: probeAnswered has started resending them, and the
timeout stays where it was, so the probe never makes recovery wait
longer than libutp's timeout would. Restarted here, each probe that
repaired one hole pushed the timeout back, and a burst came back one
packet per probe timeout.

## statePacket

`conn_ack.go`

statePacket builds an ST_STATE, the only packet that carries a selective
ack. libutp attaches the extension in send_ack alone (`pfa.pf.ext = 1`,
utp_internal.cpp:795); a data packet, a FIN and every resend go out with
`ext = 0` (:1080, :2781), and its packet size leaves room for the 20-byte
header and nothing else (:1757-1761). This library used to attach the receive buffer's selective ack to
all of them, and to reserve room for it in every packet.

On a connection carrying data both ways, that means the peer hears about a
hole in what it sent only from an ST_STATE -- and a data packet that goes
out carrying the same acknowledgement number cancels the pending one
(send_data, :768; transmit here). libutp lives with that, and so does this
library now.

## lingerAck

`conn_close.go`

lingerAck is the acknowledgement the socket should re-send if the peer keeps
retransmitting its FIN after this connection is gone.

It exists because a transfer can arrive whole and still end as a failure for
the peer. We acknowledge the peer's FIN, hand the application its end of
stream and tear down; if that acknowledgement is lost the peer retransmits
the FIN, the socket no longer has the connection, and it answers with a
RESET. libutp cannot do this in return: it keeps the socket in CS_GOT_FIN
until its own application closes, so a retransmitted FIN is simply
acknowledged again (utp_internal.cpp:2369-2370).

Measured against real libutp on a 3% loss path with reordering, before this
existed: 4 of 20 runs ended in UTP_ECONNRESET on a transfer whose every byte
had already been read.

Only a connection that reached the peer's FIN has anything to linger for.
Anything else -- a reset, an idle timeout, a local close with no FIN
received -- should still draw a RESET, which is what libutp does for a
packet it has no socket for.

## updateClosingState: the half-close

`conn_close.go`

A connection whose peer has closed its sending side is *not* finished.

This used to move to connClosed as soon as the remote FIN was reached
and everything sent had been acknowledged -- without this end ever
sending a FIN of its own, and whatever its application was doing. That
is the half-close, and refusing it was the one place libutp could do
something an application here could not: a peer closing its sending side
took the whole connection with it, its own unread data included.

libutp holds the socket in CS_GOT_FIN and keeps delivering
(utp_internal.cpp:2314 drops only what arrives past the FIN it has
reached), until its application closes too. So does this now: the reader
gets its end-of-stream marker from processReads when the receive buffer
catches up to the FIN, and the writer carries on.

Once the application *has* closed, though, this end is finished the
moment everything it sent has been acknowledged. It does not wait for
its own FIN to come back, and that is not laziness: the peer closed
first, so by the time this FIN goes out the peer's connection is
generally gone and nothing is left to acknowledge it. libutp reaches the
same place by a different route -- the peer's socket answers the
unmatched FIN with a RESET, and a socket with close_requested treats a
RESET as a clean destroy rather than an error
(utp_internal.cpp:2865-2868). This library cannot use that route,
because its socket deliberately stays silent for a packet past a closed
connection's FIN so that a peer doing a half-close is not told its
connection broke. Ending here instead reaches the same state without
needing the RESET.

Removing this entirely was the first attempt at the half-close, and the
soak test caught it: 29 connections still tracked after 60 closed
cycles, 48 goroutines grown, all of them the side that closed second.
LocalFin != nil is doing real work in this condition. shutdown() only
emits the FIN once the send buffer and the pending writes have drained,
so its presence is the proof that there is nothing left to send. Without
it, "nothing unacknowledged" is true at every momentary gap between a
window emptying and the next packet going out -- and closing there
discards the rest. Measured: the half-close test stopped delivering its
512 KB reply, because Write returns when the data is buffered and the
application closed straight after it.

## ConnectionConfig.NowMicros

`conn_config.go`

NowMicros is where this connection reads the wall clock, in the uint32
microseconds uTP puts on the wire. Defaults to nowMicro.

It governs only what is *stamped and measured* -- the timestamp field,
the timestamp difference echoed back, and the one-way delays derived
from them. Scheduling still runs on real timers: this is not a virtual
clock, and a connection given a frozen NowMicros still retransmits on
time.

It exists because libutp's driver reads a virtual clock where this
library read the real one, so the conformance corpus could not compare
the two timestamp fields at all and listed both as tolerated
differences. Pinning this to the driver's clock removes the tolerance;
see conformance_harness_test.go.

## ConnectionConfig.DelayWindow

`conn_config.go`

DelayWindow is how far back the congestion controller looks for its
base delay -- the lowest one-way delay it has seen, which it treats as
the path with an empty queue.

It is libutp's: thirteen buckets (DELAY_BASE_HISTORY,
utp_internal.cpp:50), each a thirteenth of this, so the default of
thirteen minutes is libutp's thirteen one-minute buckets exactly. See
baseDelayHist.

Configurable because the window is what bounds the damage clock drift
does: a sender whose clock loses time sees its measured delay grow
without bound, and the base only follows once the inflated samples have
aged out. The steady-state error is the window multiplied by the drift
rate, which is a relationship nothing could test while the window was a
constant two minutes. See netem.TestClockDriftInflatesTheDelaySignal.

## ConnectionConfig.NoDelay

`conn_config.go`

NoDelay sends a partial packet as soon as the window allows, as
TCP_NODELAY does. By default a connection follows libutp's Nagle rule
(flush_packets, utp_internal.cpp:974-982): a packet smaller than a
full one waits while anything else is unacknowledged, and later writes
join it, until it fills, everything before it is acknowledged, or the
write side closes.

Measured against libutp, 2,000 messages of 100 bytes written every
0.5 ms over a 20 ms round trip: with the rule, both send them in about
140 packets; with NoDelay, 2,000 packets, and each message arrives
sooner -- a median of 10.9 ms against 15.3 ms, a 99th percentile of
19.9 ms against 44.8 ms.

## lazyTimer

`conn_loop.go`

lazyTimer is a Timer made the first time it is armed. Until then its
channel is nil, which a select never takes.

A connection used to make all seven of its timers at the start, stopped,
whether it would ever arm them or not: the RACK timer without RACK, the
idle timeout without one configured, the zero-window probe without a
closed window. About 240 bytes each, held for the connection's life.

A timer made while the event loop is parked is one its select is not
watching: the loop chose its channels before the timer existed, and the
socket's reader arms timers in passes it runs inline. So making one wakes
the loop (kickLoop), which goes round and selects on it. Without that, the
first fire of a timer the reader made went unseen until something else
woke the loop -- on the virtual clock, never:
TestTailBurstLossAgainstLibutp hung.

## setupLoop: the zero-window probe and keep-alive timers

`conn_loop.go`

The zero-window probe needs a wake-up of its own.

Every other reason this loop runs is an event: a packet arrived, the
application wrote, a retransmission timer expired. A closed peer window
is the absence of all three -- nothing is outstanding, so no
retransmission timer exists, and nothing will arrive until the peer
chooses to speak. Without this the probe would be armed and never
checked.
An established connection that has gone quiet still has to say
something occasionally, or a NAT drops its mapping and the peer's own
idle timeout eventually kills it. libutp checks
`current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every timeout
pass (utp_internal.cpp:1271-1274), so its keep-alive leaves one
interval after the last packet, to within the 500ms libutp allows
between passes however often its embedder calls it
(TIMEOUT_CHECK_INTERVAL, :37, :3284).

This is a timer aimed at that instant, re-aimed from lastSentPacket
every time it fires. It used to be a ticker at the interval, counted
from connection start, which checked the same condition but only on
the tick: a connection that went quiet just after one failed the check
at the next and waited for the one after -- up to twice the interval,
58 seconds where libutp's is 29 and a NAT mapping commonly lasts 30.
Measured: 58.21s against 29.70s. See KNOWN-LIMITATIONS.md, section 2e.

## setupLoop: the idle RTO

`conn_loop.go`

An idle connection's window has to decay, and nothing else here would
wake the loop to do it.

Retransmission timers are armed per packet, so a connection with
nothing outstanding has none, and its event loop is completely
quiescent -- it does not even produce a metrics sample. libutp is not
built that way: it keeps one deadline per socket, never clears it when
the window empties (it is set at utp_internal.cpp:997, reset on each
acknowledged packet at :1389, re-armed on each expiry at :1204), and
when it passes with nothing in flight the idle branch decays the window
by a third (:1216-1222). Because `retransmit_count` is only incremented
when something *is* outstanding (:1240), that repeats without ever
killing the connection.

Measured against real libutp over the same emulated link, 8 seconds
idle after a bulk transfer: libutp's first flight afterwards was 15972
bytes against 55176 with no idle -- 29%, which is (2/3)^3, three decays
at one, three and seven RTOs. Ours was unchanged at 55375, so it would
have put a window it last measured 8 seconds ago straight back onto a
path it has not probed since. See netem.TestLibutpIdleWindowDecay.

A resting connection has no loop to wake: its rest timer is already
set for this deadline (armDormant), and is moved only if a pass the
reader ran has brought the deadline forward -- which nothing does
while there is nothing in flight, but which costs a comparison to be
sure of. Resetting it unconditionally cost a timer operation for every
read batch a resting receiver handled.

## finishLoop: reporting to the socket

`conn_loop.go`

Tell the socket to drop this connection on *every* exit, not only the
one where the state machine reached connClosed.

It used to be reported from that one branch alone, so a connection torn
down by a cancelled context -- which is what an abandoned or failed
connection attempt is -- left its entry in the socket's table forever.
Measured before the fix: 40 attempts to a dead port left 40 tracked
connections behind, permanently. A client dialling unreachable peers,
which is most of them, accumulates one of these per attempt.

The goroutines were fine; it is the map entry and its channel that
leaked, which is why nothing that counted goroutines noticed.

## afterPass: drain before acknowledging

`conn_loop.go`

Hand bytes up before acknowledging, so the acknowledgement carries
the window that results rather than the one from before the drain.

This ordering is libutp's. It hands bytes to its embedder inside
utp_process_incoming and acknowledges afterwards, which is why
utp_read_drained finds nothing to report on an ordinary packet.
Draining on a later pass instead -- which is what this loop did --
makes every drain look like the window growing, and reporting that
doubles the reverse traffic. Measured: every data packet drew two
acknowledgements instead of one, which the corpus caught at once.

ackPending is a bool and flushAck runs once per pass, so the drain
and the data that prompted it now share one acknowledgement.

What the drain will take is counted first (drainable) and advertised
as if already taken (drainCredit, recvWindow); the drain itself runs
once the acknowledgement has gone. The packets are the same either
way. What moves is the work: copying the bytes out, allocating what
the reader is handed, and waking the reader -- measured at 4-5us of
a cold reader's path from datagram to acknowledgement, which libutp,
handing its bytes up through a callback, does not spend there.

## endBatch

`conn_loop.go`

endBatch ends a read batch that delivered packets to this connection: the
pass that receiveInline left open runs now, acknowledgement included.
libutp's utp_issue_deferred_acks, which its embedder calls once the socket
would block (utp.h:512-517).

Except when the batch has left data to send. Then the whole pass is the
event loop's: one goroutine per connection does the sending, and the reader,
which serves every connection on the socket, does not. libutp sends from
inside utp_process_incoming, on its one thread; done here, the reader's
write system calls for every sender on the socket left it no time to read.
Measured with 1000 transfers on one socket pair: the sending side's reader
was busy 90-100% of the time handling 5,000 acknowledgements a second, they
waited in the kernel for seconds, RTT samples reached 17 s, and a transfer
whose last packets were lost waited out a retransmission timeout grown from
them past the test's two minutes. The acknowledgement this pass owes goes
out with the data, as it would have here.

Otherwise the event loop is woken only for what it alone can do: tear the
connection down, or go on sending past one pass's worth.

## readEndErr

`conn_read.go`

readEndErr is what the reader is told when the stream ends: the error that
ended the connection, or nil for a clean end -- and a clean end means the
reader has been given everything the peer sent, up to its FIN.

A connection that ended without error but short of that reported nil too.
The case that showed it: the consumer closes the stream while bytes are
still on their way to its reader, teardown drops them (they are owed to
nobody), and a ReadToEOF still running returned the bytes it had so far
with a nil error -- a truncated transfer reported as a complete one.
TestDataValidWhenResendingSynStateResponse saw it about once in 20 runs
under -race, before this change as after. It is ErrReadClosed now: this end
closed before it had read everything.

Once everything the peer sent before its FIN has arrived and been handed
on, the read side has ended cleanly, whatever ends the connection
afterwards. libutp has delivered every byte to its embedder by then and
signalled UTP_STATE_EOF (utp_internal.cpp:2339-2343, :2355-2357); a RESET
that follows is reported as an error but takes nothing back, and
go-libutp's reader returns the data and io.EOF before it
(conn.go, readNoWait). It happens: the side that closed first destroys
its connection once its FIN is acknowledged, and answers whatever the
other side sends next -- a window update as its reader drains -- with a
RESET (:2179-2182, :2907-2944). Here that reset turned a complete
transfer into "read: reset", one 16 MB loopback transfer in ten under
load (integrated.TestUdpTransfer).

## outsideReorderWindow

`conn_receive.go`

outsideReorderWindow reports whether a packet names a sequence number too
far from the one expected to be worth processing, and answers it the way
libutp does if so.

libutp (utp_internal.cpp:1886-1899):

    const uint seqnr = (pk_seq_nr - conn->ack_nr - 1) & SEQ_NR_MASK;
    if (seqnr >= REORDER_BUFFER_MAX_SIZE) {
        if (seqnr >= (SEQ_NR_MASK + 1) - REORDER_BUFFER_MAX_SIZE
            && pk_flags != ST_STATE) conn->schedule_ack();
        return 0;
    }

`seqnr` counts how far past the next expected packet this one is, so 0 means
"exactly the packet we are waiting for". Past 1024 the packet is either
absurdly far in the future or, by wrapping, an old one already consumed.
Either way libutp drops it without buffering it, acking it, or letting it
touch congestion control. An old packet -- within 1024 behind, and not a
STATE -- additionally re-triggers an ack, because the peer evidently missed
the one already sent.

This fork had no such bound. A peer could name any sequence number in the
16-bit space and we would buffer the packet as out-of-order data and answer
it with a STATE. Three costs: unbounded pending state driven by a remote
party, one emitted packet per junk packet where the reference emits none,
and -- since the sequence number is far outside the 30-entry selective-ack
window -- a selective ack naming nothing at all.

The check covers ST_DATA, ST_STATE and ST_FIN only. libutp reaches it
through utp_process_incoming, which a SYN returns from before the check
(utp_internal.cpp:1878) and which a RESET never enters at all: RESET is
handled in its own branch at the socket level and returns
(utp_internal.cpp:2855-2880).

Both the missing bound and the wrongly-included RESET were found by
FuzzDifferentialResponder, which runs this implementation and libutp over
the same generated packet sequences and fails when we answer something the
reference ignores.

## onPacket: measuring the peer's delay

`conn_receive.go`

Measure how long ago the peer stamped this packet. That value is what we
echo back in timestamp_difference_microseconds, and it is the peer's
only delay signal for congestion control.

This used to subtract the packet's timestamp_difference field from our
own full-width wall clock: the wrong field, and mixing an int64 epoch
clock with a uint32 wire value. The result was ~1.79e15 microseconds on
every packet, which the cap below then pinned to exactly 1 second -- so
every packet we sent advertised a constant 1,000,000 us delay and the
peer's LEDBAT controller was fed a constant. It had no delay signal at
all. See KNOWN-LIMITATIONS.md.

libutp computes reply_micro the same way, as a uint32 subtraction of the
packet's timestamp from the current time (utp_internal.cpp:2000-2002).

**It is measured here, after the validation above, and not before.**
libutp reaches `conn->reply_micro = their_delay` only once the packet
has passed the acknowledgement-number rule (:1794-1807) and the
reorder-window check (:1886-1899); a packet either of those rejects
never touches it. This did it first thing, so a packet the reference
discards still changed what we echoed to the peer -- which is the
peer's whole delay signal, and reachable by anyone who can guess a
connection id and send a packet bad enough to be dropped.

Found by FuzzDifferentialResponder once the clock became injectable:
libutp's ack carried the *previous* packet's delay where ours carried
the rejected one's. Invisible while the timestamp fields were excluded
from comparison.
Not capped. This used to pin anything above MaxIdleTimeout to exactly
one second, which was protection against the defect described above --
the wrong field, producing ~1.79e15 on every packet -- and outlived it.

libutp echoes `time - p` raw, however it wraps (utp_internal.cpp:2000).
A peer whose clock is ahead of ours produces a value that wraps to
something enormous, and that is what the reference passes on; the
receiving end has its own rule for it, not the sending end. Capping
here put a number on the wire that no libutp would have sent, and
FuzzDifferentialResponder showed it the moment the timestamp fields
could be compared: ours 1000000 against libutp's 3487502864.

A zero timestamp is no timestamp: `their_delay = (p == 0 ? 0 : time -
p)` (:2000), and a zero is neither echoed as a delay nor sampled
(:2002). This used to echo the whole of our clock back.

## onPacket: deferring the acknowledgement

`conn_receive.go`

Note that an acknowledgement is owed; do not send it here.

libutp calls `schedule_ack()` (utp_internal.cpp:2404, :2472), which
adds the socket to a list its embedder flushes once per pass of its
event loop. So a batch of packets arriving together draws one
acknowledgement, not one each.

This library used to answer every packet immediately. Measured
against libutp with the driver's deferred-ack flush: eight data
packets in one batch drew one acknowledgement from libutp and eight
from us. On an asymmetric path -- ADSL, cellular -- that reverse
traffic is not free, and on a shared bottleneck it competes with
the forward data.

## onPacket: waking the writer

`conn_receive.go`

Wake the writer on any packet that carries an acknowledgement, not only
on ST_STATE. libutp makes the socket writable again whenever an incoming
packet leaves the window below full, before the early return that is
specific to ST_STATE (utp_internal.cpp:2302-2308), so its embedder
writes on the acknowledgements a data packet carries as well.

This woke only for ST_STATE. On a connection carrying data both ways,
the peer's acknowledgements ride on its data packets -- a data packet
carrying the pending acknowledgement replaces the ST_STATE (send_data,
:768) -- so the window opened and nothing used it until a timer
happened to run. Measured: two-way transfers of 1 MB each way over
20 Mb/s, 20 ms and 5% loss took about 36 s in 3 runs of 15, against a
5 s median, one direction sitting idle with 1 MB queued, nothing in
flight and a window that had room, until the idle timeout fired.

## onState: reporting a connection

`conn_receive.go`

Report success the same way the acceptor path does, at line ~351:
send nil.

This used to close the channel instead. A closed channel and a
successful one are indistinguishable to a receiver that checks the
`ok` flag, and UtpSocket.Connect checks it -- so every successful
outgoing connection made through Connect was reported to the caller
as "connection timed out", after a handshake that had in fact
completed. ConnectWithCid happened to use the bare receive form and
so was unaffected, which is why every test in this repository
passed: they all use ConnectWithCid.

The channel is buffered with room for one, and the failure path at
onTimeout only fires while the state is still connConnecting, so
this send cannot block and cannot be followed by another.

## onTimeout: a timeout for an acknowledged packet

`conn_recovery.go`

A timeout for a packet the peer has already acknowledged is not a
timeout at all, and acting on one is what made a quiet connection
punish itself.

Timers are armed per packet and cancelled when the ack arrives, but
cancelling cannot stop one that has already fired: it is out of the
wheel and on its way to this loop, and disarmAcked will not find
it. The delivery then arrived for a packet that had been
acknowledged in the meantime -- and the early-timer guard below,
which asks only what the clock says, re-armed it. Each re-arm put
the same dead timer back in the wheel, so the armed set grew by one
per write and never shrank, and whenever several of them landed
past the deadline at once they were counted as a real timeout: the
window halved and packets the peer already had were sent again.

Measured on a link with no loss configured, after a bulk transfer
followed by 200-byte writes every 200ms: 2 timeouts, 12
retransmissions of delivered data, and the congestion window taken
from 55513 to 24831 bytes for nothing. That is the shape a
BitTorrent peer connection spends most of its life in.

libutp cannot reach this state. It has one timeout deadline rather
than a timer per packet, and it retransmits out of its outgoing
buffer (utp_internal.cpp:1230-1244), which no longer holds a packet
the peer has acknowledged.

## onTimeout: one resend per timeout

`conn_recovery.go`

One timeout per RTO expiry, measured against the clock, and one
packet resent for it.

libutp keeps a single deadline per connection and acts only when
`current_ms - rto_timeout >= 0` (utp_internal.cpp:1147-1148). Then
it marks every packet in flight need_resend, which takes their bytes
out of cur_window, and resends only the oldest (:1230-1252). The
rest go out oldest first from flush_packets, as the congestion
window -- which the timeout has just cut to one packet (:1225) --
allows, or one per acknowledgement through the fast-timeout path
(:2256-2282).

This connection arms a timer per packet, so an expiry arrives as
several callbacks. It used to resend every packet whose callback
came, on the reasoning that libutp "marks every outstanding packet
need_resend". Marking is not sending. Measured with the data
direction blacked out for three seconds (netem.TestRTOBurstMeasure):
libutp resent one packet at the timeout; this resent its whole
window, 75 packets, and most of them again a second later, because
the callbacks after the first were measured against the old
deadline and never counted as a timeout. On a path that is
congested or down, that is the opposite of what a timeout is for.

An earlier attempt at this went wrong the other way: it skipped the
other callbacks' resends and nothing replaced them, which left holes
fast retransmit could not fill, and the 5%-loss benchmark stopped
completing. What replaces them here is libutp's own two routes:
processWrites resends marked packets ahead of new data, and
fastTimeout resends the oldest on each acknowledgement.

## checkSpuriousLoss

`conn_recovery.go`

checkSpuriousLoss undoes the controller's last cut if the packet it was
for turns out to have arrived after all: RFC 3522's detection, by
timestamp. An acknowledgement echoes the timestamp of the packet that drew
it (ackEcho), so the first one to cover the packet says which copy filled
the hole. If it echoes a time before the resend was sent, the original did;
the packet was late, not lost. One that covers it before it was resent at
all says the same without a timestamp. Anything else -- the resend's echo,
a peer that reports no delay, an acknowledgement drawn by a later packet --
leaves the cut standing, so a mistake can only leave a cut that libutp
would also have made.

Every resend goes through transmit, which notes it (noteResend), so "not
resent" is not a guess.

## noteDuplicateAck

`conn_recovery.go`

noteDuplicateAck counts a peer repeating the same acknowledgement, and uses
the third one to conclude that an outstanding MTU probe was too big.

libutp: utp_internal.cpp:1921-1941. This is the second of its two routes to
lowering the MTU ceiling, and the only one that works during a bulk
transfer. The other needs the probe to be the only packet outstanding
(:1152-1167, and onTimeout), which a saturated send
window never leaves it -- so without this a probe dropped for being too big
is retransmitted fragmentable, arrives, is acknowledged, and the search
concludes the size was fine. Measured before this existed: on a 1000-byte
path with a 1400-byte ceiling, the search settled at 1384 whether or not the
probe was refused.

Three rules, all libutp's:

  - Only bare ST_STATE packets count. libutp is emphatic about why
    (:1911-1920): an ST_DATA carrying an acknowledgement was most likely sent
    because the peer had data of its own, not in response to anything, so a
    bidirectional connection would otherwise see three "duplicates"
    immediately after every payload packet it sent.
  - The acknowledgement must repeat LastAckedSeqNum, the number just before
    the oldest outstanding packet. Anything else resets the count.
  - Nothing is counted while nothing is outstanding, and the count is not
    reset then either -- libutp wraps the whole block in
    `if (cur_window_packets > 0)`.

On the third such acknowledgement, with a probe outstanding, there are two
cases and libutp draws a different conclusion from each. If the repeated
number is the one just before the probe, the probe is the hole: the ceiling
drops below it. If it is anything else, some other packet was lost ahead of
the probe, which says nothing about size -- the probe is forgotten so
another can be sent, and the ceiling is left alone.

It acts once per run of duplicates -- a run ends when the acknowledgement
moves -- on the first duplicate that finds the count at three or more. That
is the one place this differs from libutp, whose test is equality with three
(`duplicate_ack == DUPLICATE_ACKS_BEFORE_RESEND`, :1928). The selective ack
sets the count (noteSelectiveAckCount), and an acknowledgement covering a
read batch can name more than three later packets at once: the count jumps
past three without ever equalling it, and libutp never concludes anything
from that run. Its own embedders batch reads that way, and so does ours on a
real socket. Measured over netem with a receiver that batches as a real
socket does: TestDontFragmentBringsTheSearchWithinThePath failed 9 runs in
10 under the equality test, the search never once hearing that a probe was
refused. Three or more is the same evidence -- at least three packets after
the hole arrived and the hole did not -- that libutp's selective ack already
resends on (`count >= DUPLICATE_ACKS_BEFORE_RESEND`, :1538).

## processWrites: composing packets

`conn_send.go`

Compose data packets.

libutp queues a write as packets of up to packet_size and flush_packets
sends the next one while !is_full() (utp_internal.cpp:3197-3212,
:963-985). is_full is called there with no argument, so it charges a
whole packet_size whatever the queued packet actually holds (:933-934),
and refuses when cur_window + packet_size > min(max_window, opt_sndbuf,
max_window_user) (:936, :956). Two things follow, and both used to be
different here:

  - Bytes in flight count against the peer's window, not only against
    the congestion window. The peer's advertisement is how much it can
    take beyond what it has already acknowledged; everything we have
    sent since is already spending it. Without this we overran a
    receiver's buffer by up to a full window whenever its application
    fell behind, and every packet past its room was dropped and had to
    be retransmitted.
  - Nothing is sent into less than a full packet of room, and a packet
    is never cut down to fit what room there is. We used to fill a
    window to the byte with whatever size fitted.

opt_sndbuf has no counterpart: our send buffer bounds what the
application may queue, not what may be in flight.

## transmit: the MTU probe

`conn_send.go`

Use this packet as an MTU probe if the search wants one.

libutp decides the same thing in send_packet (utp_internal.cpp:906-925)
and sends the probe with fragmentation disabled, by passing
UTP_UDP_DONTFRAG to its embedder's sendto callback (:925-929). This
carries the same bit the same way: the flag travels with the datagram to
the socket, which asks the Conn to honour it if it can.

A Conn that cannot -- the libutp driver, an emulated network that does
not model fragmentation, an operating system with no such socket option
-- sends it normally, which is what every probe did before this existed.
The cost of that is real and is why the bit is worth carrying: on IPv4 a
router may fragment an oversized probe rather than drop it, so the probe
is acknowledged, the floor rises, and the search settles on a size that
works only because every packet at it is being fragmented.

## timeWheel

`delay_set.go`

timeWheel is a hashed timing wheel.

Two properties matter for retransmission timing, and the original
implementation had neither:

  - Resolution. A delay shorter than one tick used to land in the slot
    being processed next, so it fired somewhere in [0, interval) rather
    than at the requested time. With a one-second interval and a 500 ms
    RTO, that declared packets lost long before their ack could arrive --
    about 6% of them on a lossless path. An item now fires on the first
    tick at or after its deadline: never early, and less than one interval
    late.
  - Range. Delays beyond slotNum*interval used to be clamped to that
    ceiling, so exponential RTO backoff stopped growing. The rounds
    counter removes the ceiling.

Removal is O(1): an index maps each key to the slot holding it. Scanning
every slot was acceptable for a per-connection wheel but not for one shared
across a whole socket.
It is generic in its key as well as its value, and keeps items by value,
so that scheduling one costs no allocation: with an interface key and a
pointer per item, the retransmission wheel allocated three objects for every
packet sent.

## timeWheel.put

`delay_set.go`

put schedules value to expire after delay. Re-putting an existing key
reschedules it.

The item goes in the slot whose tick is the first due at or after the
deadline, to the millisecond, so it never fires early and fires less than
one interval late. To the millisecond is libutp's resolution: its clock is
current_ms, a deadline is current_ms plus the timeout, and it fires once
(int)(current_ms - rto_timeout) >= 0 (utp_internal.cpp:997, :1147, :1389),
which can be up to a millisecond before the microsecond the deadline was
set for.

It used to count whole intervals from now, rounded up, and add one more,
because the wheel ticks on its own schedule and "n ticks from now" could be
as little as n-1 intervals away. Placing it n intervals past the slot about
to be processed made it never early -- measured against libutp before that:
a SYN with a 200 ms timeout retransmitted at 186 ms -- but nearly a whole
interval late whenever the put came just after a tick. A retransmission
re-arms its timer from the tick that fired it, so every backoff landed one
25 ms tick later than the last: a 200 ms timeout's retransmissions at 208,
633, 1458 and 3084 ms against 200, 600, 1400 and 3000. Placing by deadline
to the microsecond did no better, because the re-arm runs a fraction of a
millisecond after the tick and its deadline lands just past the matching
one.
wheelResolution is how precisely a deadline is honoured: libutp's
millisecond. See put.

## ConnectionMetrics.QueueingDelay

`metrics.go`

QueueingDelay estimates the standing queue on the path this connection is
sending into, as the excess of the current one-way delay over the lowest one
seen. This is the quantity LEDBAT exists to bound.

Both terms come from the same series and the same direction -- the peer's
measurement of our outbound path. **They did not.** This subtracted
BaseDelay, an outbound figure, from PeerTsDiff, an inbound one, and the
difference between two directions is not a queue in either of them. On an
asymmetric path -- which is most paths, and every consumer broadband link --
it was a constant offset with the actual queue buried in it.

The congestion controller was never affected: it compares the current
sample against the base from the same series, in hand at the point it acts
(congestion.go, defaultController.OnAck). What this fed was the queueing
delay netem's Recorder reports, which is diagnostic.

The comment here also used to claim this was "the number the M5 gates are
judged on". It was not: those gates read the emulated link's own
MeanQueueDelay, measured at the bottleneck rather than inferred from
timestamps, which is why they stayed sound while this did not.

It is only meaningful once BaseDelay has been established; before then it
returns zero.

## mtu.go

`mtu.go`

Path MTU discovery, as libutp performs it.

A uTP sender has to choose a datagram size without being told what the path
carries. Too small wastes header overhead on every packet; too large is
worse than wasteful, because a datagram above the path's MTU is either
fragmented -- costing more than it saves -- or, on IPv6 and on any IPv4 path
with the don't-fragment bit set, silently dropped. A connection that picks
too large a size and cannot detect it does not run slowly; it stops.

That is why this library shipped a fixed 1024 rather than libutp's ~1400:
without discovery, the only safe fixed size is a small one. Measured, the
difference is +2.4% on a long transfer and +17% on a reordering profile --
worth having, but not worth a connection that fails outright on a PPPoE or
VPN path. Discovery is what makes the larger size safe to reach for.

The algorithm is a binary search between a floor known to work and a ceiling
known or assumed not to. Each probe is an ordinary data packet, sized at the
midpoint and sent with fragmentation disabled where the platform allows it:

  - acknowledged, and the floor rises to the probe's size;
  - lost, and the ceiling drops to just below it.

The search ends when the two are within mtuSearchDoneThreshold of each
other, and restarts every mtuSearchInterval because paths change.

libutp: mtu_search_update (utp_internal.cpp:1289-1312), mtu_reset
(:1314-1322), the probe decision in send_packet (:890-925), the
probe-acknowledged path (:1969-1974), and the two failure paths --
retransmission timeout (:1152-1167) and duplicate acknowledgements
(:1927-1940).

Both failure paths are implemented: the timeout one in connection's
retransmission handling, and the duplicate-acknowledgement one in
connection.noteDuplicateAck. The second is not optional -- the first
requires the probe to be the only packet outstanding, which a saturated
send window never leaves it, so without the second a probe dropped for
being too big is retransmitted fragmentable, arrives, and is read as the
size being fine.

## newMtuSearch

`mtu.go`

newMtuSearch starts a search bounded above by ceiling, sending at the
ceiling, as libutp does: `mtu_last = mtu_ceiling` when a socket is created
(utp_internal.cpp:2561-2562). The first packet larger than the floor is
the probe; if it is lost the ceiling comes down and the search proceeds
from the midpoint.

This used to start at the midpoint, 989 bytes against a 1402 ceiling, so
that an untested path got a small packet. libutp's start was faster on
every healthy path measured (DEVIATIONS.md, "The MTU search starts at the
ceiling"), which is nearly every path, and the ceiling itself is libutp's
conservative 1402 (or the local interface's, or 1232 for IPv6), which
leaves room for a tunnel.

## mtuSearch.clearProbe

`mtu.go`

clearProbe forgets the outstanding probe without drawing any conclusion from
it, so another can be sent.

libutp does this on every retransmission timeout, outside the branch that
lowers the ceiling: `// we dropped the probe, clear these fields to allow us
to send a new one` followed by `mtu_probe_seq = mtu_probe_size = 0`
(utp_internal.cpp:1166-1167).

The distinction matters and its absence was a stuck search. The ceiling may
only come down when the probe was the *only* packet outstanding, because
only then does its loss say something about size rather than congestion. A
probe lost alongside other packets says nothing -- but it is still gone, and
if the search goes on waiting for it, it waits forever. Measured against a
path with a 1100-byte limit: the search parked at 1191 bytes, every packet
was refused for size, and a 1MB transfer delivered 4KB before the test timed
out at 60s.

## selectiveAck.Encode

`packet.go`

Encode encodes the SelectiveAck into a byte slice
Encode renders the selective-ack bitmask.

Bit order is least-significant-first within each byte: the LSB of the first
byte represents ack_nr+2, the next bit ack_nr+3, and so on. That is what
BEP 29 specifies and what libutp emits -- it builds the mask with
`m |= 1 << i` for the i'th packet past ack_nr+2 and writes the low byte
first (utp_internal.cpp:806-818).

This previously set `1 << (7-j)`, reversing the bits within every byte. The
decoder below reversed them the same way, so this implementation agreed
with itself and Go-to-Go transfers were unaffected -- but every selective
ack it sent was misread by any real peer, and every one it received was
misread in turn. Only a comparison against libutp surfaces a bug that is
self-consistent.

## packet.wireExtensions

`packet.go`

wireExtensions is the extension chain Encode writes, in order.

A packet built here carries at most a selective ack. A decoded packet
carries every extension it arrived with, in the order it arrived, so that
decoding and encoding again gives back the same bytes: unknown types
further along the chain, which both this decoder and libutp step over
(utp_internal.cpp:1844-1866), and extension 2, whose contents nothing here
reads. These used to be dropped. Nothing on the live path re-encodes a
received packet, and libutp never does, but the decoder was then exported,
and anything that forwarded packets through it would have stripped what it
did not understand.

The selective ack keeps its place in the chain, and its bytes come from
Eack, so a caller that changes Eack changes what is written. Its place is
the last type-1 extension, because that is the one the decoder reads, as
libutp's loop does. If Eack is nil that entry is dropped -- unless it was
empty, in which case it never became an Eack and is kept as it came.

An unknown type is never written first. A packet whose first extension is
unknown is rejected by this decoder and by libutp's version check
(utp_internal.cpp:2480), so leading with one would make the packet
unreadable. It can only happen if a caller cleared the selective ack that
used to come first, and then the unknown extensions ahead of the first
known one are dropped.

## packet.Encode

`packet.go`

Encode writes the packet to the wire.

The header's extension byte is derived from what is actually being written
rather than taken from p.Header, so that everything this produces can be
decoded again.

It used to be taken from p.Header, which is only correct for packets built
by packetBuilder. A *decoded* packet keeps whatever extension byte the peer
sent, while Eack is nil unless a selective ack was retained -- so a packet
that arrived carrying a zero-length selective ack, or the extension-bits
extension, re-encoded to a header claiming an extension with no extension
bytes after it. This implementation's own decoder rejects that, with
"insufficient length for extension". Found by FuzzDecodePacket.

Nothing on the live path re-encodes a decoded packet today, so this was
latent -- but the decoder was then exported and its result had exported
methods, so an external caller could reach it, and a proxy or relay built on
this library would have emitted unparseable packets.

## PathMTUProvider

`path_mtu.go`

PathMTUProvider is implemented by a Conn that can say how large a datagram
the local path towards a peer will carry.

It is libutp's UTP_GET_UDP_MTU callback (utp.h:78), which `mtu_reset` uses
to set the ceiling the path-MTU search starts from
(utp_internal.cpp:1314-1322). This library had no equivalent: the ceiling
was a fixed 1400 for every connection, which is safe on an ordinary path
and too large on a tunnelled one -- and a ceiling above what the path
carries is the stall recorded in KNOWN-LIMITATIONS.md.

The Conn interface deliberately has only ReadFrom, WriteTo and Close, so
that an emulated network and the libutp driver satisfy it as easily as a
UDP socket. Asking an operating system about an interface has no place
there. This is the optional extension instead: a Conn that can answer
implements it, everything else is unaffected and keeps the configured
ceiling.

The value returned is the largest **uTP datagram** the path will carry --
what fits in a UDP payload -- not the link MTU. That is the same quantity
libutp's callback returns, and the same one mtuSearch works in.

It is only ever used to *lower* the configured MaxPacketSize, never to
raise it. A loopback interface reports 65536, and nothing here wants a
65-kilobyte datagram.

## pathMTUCeiling

`path_mtu.go`

pathMTUCeiling is the datagram size a new connection to peer should start
its search from: the configured ceiling, lowered to what the local path
will carry when the Conn can say.

Lowering only. libutp takes get_udp_mtu as the ceiling outright, which on
loopback is 65488 and on a jumbo-frame link 8972; MaxPacketSize stays a
hard cap here because a ceiling that high is a probe schedule this library
has never been measured at, and raising it is a separate decision from
fixing the case where 1400 is too big. Recorded in DEVIATIONS.md.
ipv6SafeDatagram is the largest uTP datagram every IPv6 path carries: the
1280-byte IPv6 minimum link MTU less 40 bytes of IPv6 header and 8 of UDP.
libutp's UDP_TEREDO_MTU, the path MTU its default callback reports for any
IPv6 peer (utp_utils.cpp:222, :230, :232-235).

## ipv6Ceiling

`path_mtu.go`

ipv6Ceiling caps the ceiling for a peer whose address is IPv6 at
ipv6SafeDatagram, as libutp does.

libutp's comment calls it conservative -- "Since we don't know the local
address of the interface, be conservative and assume all IPv6 connections
are Teredo" -- and it is, but it is also what keeps it from stalling. IPv6
routers do not fragment, and with ICMPv6 filtered an oversized packet
simply vanishes: the "path MTU below the size already adopted" stall in
KNOWN-LIMITATIONS.md. Measured over a 1280-byte IPv6 link with no ICMP,
1 MB, five runs each: this library at its 1400 ceiling stalled every time,
and at 1232 delivered 6.17-6.22 Mbps. Over a 1500-byte IPv6 link the cap
cost nothing measurable, 6.20-6.23 against 6.22-6.26 Mbps.

An IPv4-mapped address travels as IPv4 and is left alone, as is any peer
that does not carry an address.

## receiveBuffer.capacity

`recv_buffer.go`

capacity is the configured size: what the window is advertised from
and admission is checked against. buf grows towards it as data
arrives, and holds the offset bytes not yet read at buf[head:].

buf was allocated at the full capacity up front -- 1 MB by default --
so every connection held a megabyte from the moment it was set up,
whether it ever carried data or not: 1.08 MB per connection end that
exchanged 1 KB, measured, where libutp holds about 10 KB per socket,
its buffers growing as they are used (SizableCircularBuffer::grow,
utp_internal.cpp:185-194, behind inbuf and outbuf at :549). With no idle timeout
by default, that was a megabyte held for every vanished peer an
application did not close. Like libutp's, it grows and is kept.

## receiveBuffer.gapReserve

`recv_buffer.go`

gapReserve is the room held back from data arriving out of order, so that
the packet which fills the gap can always be admitted.

Without it a gap is a deadlock. Data behind the gap is accepted until the
buffer is full, and then the one packet that would release all of it -- the
retransmission of the missing packet -- is refused for want of space. It is
refused every time the peer sends it, and the peer sends it forever.

Measured before this existed, on a 32KB receive buffer with a reader that
paused for two seconds: `pending 32613, readable 0, held-behind-gap 32613`,
unchanged for the rest of the run while packets kept arriving and being
dropped. A 512KB transfer delivered 152KB and stopped. It reached that state
in about 3 runs in 20.

The figure is the largest packet this peer has sent so far rather than a
constant, because that is exactly what the gap-filling retransmission will
be: the peer is resending a packet it already sent, and every packet it has
sent is at most this. It costs that much of the buffer, once.

## receiveBuffer.Window

`recv_buffer.go`

Window is the receive window to advertise to the peer: the capacity, less
the bytes already delivered in order and not yet handed up.

Deliberately not Available(). Data held out of order is not counted against
it, which is libutp's accounting and was not this library's:

    UTPSocket::get_rcv_window  (utp_internal.cpp:590-596)
        const size_t numbuf = utp_call_get_read_buffer_size(this->ctx, this);
        return opt_rcvbuf > numbuf ? opt_rcvbuf - numbuf : 0;

libutp's window is what its embedder has not yet read. A packet held out of
order sits in conn->inbuf and never reaches utp_call_on_read until the gap
before it is filled, so it never enters that figure.

Charging it, as Available() does, closes the window on the peer for holding
data the peer was entitled to send -- and the window is what permits the
peer to send, including to retransmit the very packet that would fill the
gap and let all of it be delivered. Measured before this split: 40,000
bytes held behind a gap cost exactly 40,000 of advertised window where
libutp lost none, and 800 packets drove it to 1,376 bytes, under the size of
one packet and above the zero that would have armed the peer's zero-window
probe. Both ends then sit silent. See KNOWN-LIMITATIONS.md.

# Why this does not overrun the buffer

Available() still charges the out-of-order bytes, and admission still uses
it, so offset plus pending stays within the capacity -- but for the one
packet Admits takes while nothing is readable, which appendInOrder grows
the storage for.

A peer respecting this window cannot break it either: the window is the
capacity less what has been delivered, so everything the peer is permitted
to send fits in what is left, whether it lands in order or behind a gap. A
peer ignoring the window is caught by admission, which drops rather than
writes. Over-advertising therefore costs a retransmission, never a panic.

## receiveBuffer.SelectiveAck: the window

`recv_buffer.go`

A fixed window, as libutp uses, rather than one sized to reach the
highest pending sequence number.

This previously grew until every pending packet was covered, up to 2016
bits. A peer that leaves a gap open and then delivers a packet far past
it -- reordering, or deliberately -- made us answer every subsequent
packet with a 254-byte extension where libutp answers with six bytes.
The extra bits carry real information, but libutp does not send them
and recovers by letting the window slide forward as the gap fills, so
sending them buys nothing against a libutp peer and costs bandwidth on
the ack path exactly when the path is already in trouble.

Only the window is visited. Every packet held used to be copied into a
map first, on every acknowledgement, to look up the window's few
dozen: work and an allocation growing with the reorder buffer, which
libutp's fixed-size scan of its own (utp_internal.cpp:800-830) does not
have. Release review of 9c44c4f.

## defaultRetransmitTickInterval

`retransmit_timer.go`

defaultRetransmitTickInterval is the resolution of the retransmission
timer wheel.

This bounds how late a retransmission can be, and -- more importantly --
it used to bound how *early* one could be. The wheel was previously
built per connection with an interval of InitialTimeout/4, one second,
which cannot express a 500 ms RTO at all: the timer landed on the next
tick, uniformly 0-1000 ms away, so roughly 6% of packets on a lossless
path were declared lost before their ack could arrive.

25 ms is fine enough that quantisation is negligible against any
realistic RTO. It is affordable only because the wheel is shared across
the whole socket: one ticker, not one per connection.

## sendBuffer.Write

`send_buffer.go`

Write copies data into the buffer and reports how much it took.

The copy is not an optimisation to remove. This buffer used to retain the
caller's slice, and UtpStream.Write returns as soon as the bytes are
accepted here -- before they are transmitted. So the caller got control back
while the connection still held a reference to their array, and anything
that reuses its buffer between writes had its queued bytes overwritten with
whatever it read next.

That is not an exotic pattern: io.Writer's contract says "Implementations
must not retain p", and io.Copy, bufio.Writer and every echo loop rely on
it. It showed up as corrupted payloads in any full-duplex exchange -- read
into a buffer, write it back, repeat -- while every unidirectional test in
this repository passed, because they each write one buffer once and never
touch it again.

## sentPackets.packets

`sent_packet.go`

packets is the send window: packets[0] is sequence number base, and
each one after it the next. Acknowledged packets at the front are
dropped when a new one is added (trimAcked), so this holds what is
outstanding and little else -- libutp's outbuf, which holds
cur_window_packets (utp_internal.cpp:1071-1077).

It used to hold every packet the connection had ever sent, indexed by
distance from the initial sequence number modulo 65536. Past 65,535
packets -- about 90 MB -- a new sequence number landed on an old,
acknowledged entry: the new packet was booked as a retransmission of
it, bytes in flight stopped counting it, and the sender ran away at
the full rate of its event loop, measured at 22,000 packets a second
into a 10 Mbps link. Nothing acknowledged was ever freed either.

## sentPackets.fastResendSeqNum

`sent_packet.go`

fastResendSeqNum is the lowest sequence number still eligible for fast
retransmission. Everything below it has either been acked or been fast
retransmitted once already, and only the retransmission timer will send
it again.

This is libutp's `fast_resend_seq_nr` (utp_internal.cpp:470), advanced
past each packet as it is resent (:1603) and forward with the
cumulative ack (:2186-2188), and tested before any fast retransmit
(:1537, :1560).

Without it a packet declared lost stayed lost until its own ack came
back, and every ack that arrived in the meantime resent it again and
halved the window again. Measured on a 2%-loss link: 52 fast
retransmissions for 7 distinct lost packets, one packet sent 16 extra
times.

## handleNewAcceptEvent: waiting

`socket_accept.go`

Otherwise wait for one.

This used to fail outright with "no incoming conn" when nothing had
arrived yet, which made Accept unusable for the ordinary server
pattern: call Accept, then have a client connect. The SYN that arrived
a moment later was parked in incomingConns with nobody left to claim
it, and the peer retried until it gave up. The cid-specific path has
always queued the request; this one now does too.

Those already abandoned go first. They were dropped only when a SYN
came to claim one, so on a socket nobody connected to, an accept loop
with a timeout grew this list by one per timeout for as long as it
ran (release review of 9c44c4f, R7). Pruned on each arrival, it holds
at most the live requests and those abandoned since the last one.

## routeCandidates

`socket_routing.go`

routeCandidates is the connections, as (send, recv) id pairs, that a packet
can be for, in the order they are tried. libutp's lookups, exactly:

  - A SYN carries the sender's receive id, ours to send to, and is matched
    only as a retransmission of one already taken: receive id one above it
    (utp_internal.cpp:2957). The other derivations alias a SYN onto
    whatever local connection holds that number, and a SYN delivered into
    an established connection was answered with a RESET -- found by
    FuzzDifferentialInitiator; an off-path attacker who guessed an id could
    have injected one.
  - A RESET may carry either of our ids, so it is matched on the receive
    id first and then on the send id (:2856-2859).
  - Anything else carries our receive id and is matched on it alone
    (:2884-2892). Receive ids are unique per peer (GenerateCid, and the
    SYN check in handleIncomingBuf), so at most one connection matches:
    ours if we dialled (send one above), the peer's if we accepted (send
    one below).

Every packet used to be tried as if its id were also our *send* id, first.
Two connections to one peer whose ids are adjacent -- one receiving on R
and sending on R+1, the next receiving on R+1 -- then share a number, and
a packet for the second matched the first: its SYN-ACK went into the
first connection as an acknowledgement outside the window, and the second
never connected (TestPacketReachesTheConnectionItIsFor). The long soak drew
such a pair once every few thousand connections.

## newUtpStream: the connection's lock

`stream.go`

The connection's lock is taken here and released by its event loop once
it has set the connection up, so a packet the socket's reader brings
before then waits for the setup rather than finding it half done.

It used to be taken by the event loop itself, and this waited for the
setup to finish -- with the socket's dispatch lock held, since the
connection has to be registered atomically. That put a goroutine's
scheduling latency in front of every packet on the socket, once per new
connection: with 1000 connections starting together the acknowledgements
of those already running were held up for seconds, their RTT samples
reached 15 s, and a retransmission timeout grown from them stalled a
transfer past TestManyConcurrentTransfers' two minutes.

## UtpStream.Write: the copy

`stream.go`

The connection goroutine keeps hold of what is queued here until it has
copied it into the send buffer, which may be long after this call
returns: a Write that hits its deadline returns on ctx.Done below with
its entry still in the connection's pending list. The caller is then
free to reuse its buffer -- net.Conn makes no promise otherwise -- and
the connection would copy whatever it had become.

That is not only a race detector complaint. The bytes actually sent
would be the mutated ones, silently, on a write the caller had already
been told failed. Found by nettest.RacyWrite under -race, which mutates
the buffer immediately after every Write; nothing in this repository had
reason to do that.

libutp does not retain either: utp_writev copies out of the caller's
iovec into packet buffers before it returns (utp_internal.cpp:1057-1066),
so an embedder may reuse its buffer straight away. One copy per Write is
what that costs, and on the fastest link the harness models it does not
show up at all -- see "What the write copy cost" in BENCHMARKS.md.

## UtpStream.writeChunked

`stream.go`

writeChunked writes total bytes, which fill copies out of the caller's
buffers, a chunk at a time: each copied once the last has been taken.

A write used to copy everything it was given before queueing any of it, so
a 100 MB write held 100 MB on top of the caller's own, however small the
send buffer it was going into -- and concurrent writes each held theirs.
Now a write holds at most one send buffer's worth beyond what is already
in the send buffer. Release review of 9c44c4f.

writeMu keeps the chunks of one write together: concurrent writes are each
still written whole, one after another, as they were when each was one
queued write.

## UtpStream.Close: returning at once

`stream.go`

Return, and let the connection finish in the background: libutp's
utp_close queues the FIN, sets close_requested and returns
(utp_internal.cpp:3358-3380), and the socket goes on sending until
utp_check_timeouts retires it.

This used to wait for the connection to flush what was queued,
while its peer was still answering (waitForFlush): Write returns
once the data is in the send buffer, so Write then Close then exit
would otherwise lose the tail. That wait now happens when the
socket is closed (UtpSocket.awaitLingering), which is where an
exiting program loses whatever was still in flight; closing a
stream never holds its caller. Measured before: a 512 KB tail over
2 Mbps held Close for 2.3 s.

## nowMicro

`utp_utils.go`

nowMicro is the timestamp uTP puts on the wire: microseconds, truncated to
32 bits, from a clock that never jumps.

libutp stamps packets from CLOCK_MONOTONIC (utp_utils.cpp:158-175), and
guards even that against running backwards. This used the wall clock,
time.Now().UnixMicro(), which an NTP step moves by however far it is
wrong. The peer reads the difference between our stamp and its own clock
as the one-way delay, so a step back of a second became a second of queue,
or a delay below the base that the base then followed down and kept.

The value is the wall clock as it stood when the process started, carried
forward by the monotonic clock: the same number time.Now().UnixMicro() would
give while nobody touches the clock, and unaffected when somebody does. Only
differences between stamps carry meaning, so where it starts does not matter.

## utpnet Socket.WriteTo

`utpnet/socket.go`

WriteTo sends a datagram, unchanged, to addr.

It is the outbound half of sharing the port: a caller's DHT replies go out
through the same socket the uTP connections use.

The write deadline applies for as long as the write waits, and moving it
moves the wait, as net.PacketConn's contract says. The datagram is handed
to one writer goroutine (see passthroughWriter), so the wait can end
without the write: the port is shared with the uTP connections, and a
deadline set on the port itself would end their writes too. A datagram
whose deadline passes before the writer reaches it is not sent; one the
writer is already sending may still be, as one already given to the kernel
may. p is copied, so the caller has it back on return either way.

It used to check the deadline once and then write directly, so a write
that blocked -- on a full send buffer, or a supplied PacketConn that
blocks -- ignored both the deadline and any move of it. Release review of
9c44c4f (R8).

A UDP socket with no deadline set is still written directly: there is
nothing to time out, and handing over costs 4us and eleven allocations a
datagram (BenchmarkWriteTo). The one thing that gives up is a deadline set
while such a write is blocked, which waits for the kernel's send buffer --
draining at the interface's rate -- rather than ending at once.

## connection.onReadDrained

`conn_read.go`

onReadDrained tells the peer when handing bytes up has reopened a receive
window smaller than the one now available.

libutp's utp_read_drained (utp_internal.cpp:3242-3261), which its embedder
calls whenever it has drained the socket:

    const size_t rcvwin = conn->get_rcv_window();
    if (rcvwin > conn->last_rcv_win) {
        if (conn->last_rcv_win == 0) conn->send_ack();
        else conn->schedule_ack();
    }

Without it the peer finds out only from the next acknowledgement this end
happens to send -- and while it is blocked on a window too small to fit a
packet it sends nothing, so nothing draws one.

**The test is growth, not growth from zero, and the difference is a hang.**
An earlier version of this fired only when the window last advertised was
zero, on the reasoning that an ordinary drain is already covered by the
acknowledgement owed for the data. It deadlocked one run in three. The
trace: the window fell to 861 bytes, which is not zero but is less than a
packet, so the sender could not fit one and went quiet; nothing arrived, so
nothing was acknowledged; the application read and freed the buffer, and the
zero-only test declined to mention it. Both ends sat silent for seconds at a
time and a 512KB transfer delivered 154KB before the test gave up. A window
too small to use is as blocking as a closed one, and libutp's condition
covers both because it compares against what was actually last sent.

libutp's distinction between acknowledging now and scheduling is not
preserved, because it does not exist here: flushAck runs at the end of this
same pass either way.

## connection.ackPathSelfQueued

`conn_ack.go`

ackPathSelfQueued reports whether the queue on the way back is this
receiver's own acknowledgements, as far as can be told: it has been seen,
within ackRateMemory, climbing steadily out of its own noise. The delay is
sampled every ackQueueSampleEvery; it is climbing steadily when it has
climbed twice in a row: the least of a window of ackQueueWindow samples
above the greatest of the window ackQueueSpan samples before it by
ackQueueClimb or more, and that window likewise above the one before it.
See ackEvery.

Acknowledgements a return path cannot carry pile up steadily: over a
160 kb/s return path, with a 20 Mb/s flow in slow start, the delay read
for the way back climbed from nothing to 43 ms in 1.2 s, about 2 ms every
25 ms, with small dips. Another flow's queue does not climb like that
under this one's acknowledgements: with a loss-based flow sharing the
bottleneck, the delay swung between nothing and 50-80 ms from one 50 ms
sample to the next, and a window of those holds a sample near nothing.

Twice in a row, because the delay also steps when a host is busy. Every
timestamp it is read from is taken by a process that may be waiting for
a CPU, and with every package's tests running at once one climb -- one
window 2.7 to 7.7 ms above the one before -- was seen in four runs of ten
behind a loss-based flow. Each made this receiver coalesce for
ackRateMemory: 0.28-0.36 acknowledgements per data packet against
libutp's 0.97, which yields less. A step is one climb and then level; a
queue of acknowledgements climbs the whole time. Under the same load the
two-climb rule fired in none of ten
(netem.TestReceiverDoesNotCoalesceBehindAnotherFlow). It sees a real
queue later, and costs a little where coalescing pays: 4 MB from libutp
over a 64 kb/s return path in 2.53 s against the one-climb rule's 2.47,
over 160 kb/s 2.09 against 2.03, at 320 kb/s the same (ten interleaved
runs each; libutp's own receiver takes 7.32 and 3.0 s). Windows eight
samples apart rather than six took 2.62 and 2.12.

Four simpler tests were tried before the one-climb rule and failed:
whether coalescing halved the queue (those swings halve on their own),
whether the queue came back while coalescing, whether it fell between the
halves of a second of coalescing (over 64 kb/s the sender is still in slow
start, its acknowledgements growing faster than coalescing takes them),
and whether it rose by 5 ms in each of four samples (it rises by about 2).
