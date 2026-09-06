package netem

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// A loss-based, Reno-shaped competing flow.
//
// LEDBAT exists to yield to TCP. Nothing in this harness could test that,
// because there was no TCP-like traffic to yield to -- every measurement was
// uTP against uTP, which is the easy case and not the one the protocol is
// judged on. This provides the other side of that experiment.
//
// # What this is
//
// A sender and receiver implementing TCP Reno's congestion control, exactly:
// slow start, congestion avoidance at one segment per round trip, fast
// retransmit on three duplicate acknowledgements, fast recovery halving the
// window, and a retransmission timeout that collapses it. The round-trip
// estimator is Jacobson/Karels, as RFC 6298 specifies, with Karn's algorithm.
//
// Congestion control is the whole point: what makes TCP the traffic uTP must
// defer to is that it fills the bottleneck queue and only backs off when
// packets are dropped. That behaviour is reproduced faithfully.
//
// # What this is not
//
// It is not TCP. There is no TCP header, no handshake, no options, no window
// scaling, no SACK, no Nagle, no delayed acknowledgements, no PAWS, no
// checksums, and no interoperability with anything. The receiver acknowledges
// every packet immediately; a real TCP receiver acknowledges every second
// packet or on a delay timer, which makes real TCP grow marginally more
// slowly than this does.
//
// So a result from it should be read as "against a loss-based sender that
// fills the queue", which is the property that matters here, and not as
// "against Linux TCP". Where this file's behaviour is more aggressive than
// real TCP -- the acknowledgement policy is the one place it is -- a uTP flow
// measured against it is being tested slightly harder than reality, which is
// the safe direction for a deference claim.

const (
	// renoHeaderBytes is a sequence number and a flag.
	renoHeaderBytes = 8
	// renoInitialWindowSegments is TCP's initial window. RFC 6928 raised the
	// usual value to 10; the conservative 2 is used here so the comparison is
	// not decided by the first round trip.
	renoInitialWindowSegments = 2
	// renoDupAcksBeforeRetransmit is Reno's three duplicate acknowledgements.
	renoDupAcksBeforeRetransmit = 3
	// renoMinRTO is RFC 6298's one-second floor, relaxed to 200ms because the
	// emulated paths here are tens of milliseconds and a one-second floor
	// would make a timeout dominate every measurement. Documented rather than
	// silent: it makes this sender recover from loss faster than a
	// standards-compliant TCP would.
	renoMinRTO = 200 * time.Millisecond
	renoMaxRTO = 10 * time.Second
)

const (
	renoFlagData = 0
	renoFlagAck  = 1
	renoFlagFin  = 2
)

// RenoResult is what a completed Reno flow achieved.
type RenoResult struct {
	BytesTransferred int
	Elapsed          time.Duration
	Goodput          Throughput
	PacketsSent      uint64
	Retransmits      uint64
	Timeouts         uint64
	FastRetransmits  uint64
	// CwndMaxBytes and CwndMeanBytes describe the congestion window over the
	// transfer, sampled once per acknowledgement.
	CwndMaxBytes  uint32
	CwndMeanBytes uint64
}

// RunRenoFlow sends payloadBytes from sender to receiver over the emulated
// network and returns what it achieved. It blocks until the transfer
// completes or ctx is done.
//
// mss is the segment size, excluding this protocol's 8-byte header.
func RunRenoFlow(
	ctx context.Context,
	sender, receiver *Endpoint,
	payloadBytes int,
	mss int,
) (*RenoResult, error) {
	if mss <= 0 {
		mss = 1024
	}

	recvDone := make(chan int, 1)
	recvCtx, stopReceiver := context.WithCancel(ctx)
	defer stopReceiver()
	go renoReceiver(recvCtx, receiver, sender.Addr(), recvDone)

	s := &renoSender{
		ep:       sender,
		dst:      receiver.Addr(),
		mss:      mss,
		total:    payloadBytes,
		cwnd:     uint32(renoInitialWindowSegments * mss),
		ssthresh: 1 << 30,
		rto:      time.Second,
		inflight: make(map[uint32]time.Time),
	}
	return s.run(ctx, recvDone)
}

type renoSender struct {
	ep  *Endpoint
	dst utp.ConnectionPeer
	mss int

	total int
	sent  int

	cwnd     uint32
	ssthresh uint32
	inFlight uint32

	nextSeq    uint32
	highestAck uint32
	dupAcks    int

	srtt, rttvar, rto time.Duration

	inflight map[uint32]time.Time
	// retransmitted marks sequence numbers whose round trip must not be
	// sampled: Karn's algorithm.
	retransmitted map[uint32]bool

	stats                RenoResult
	cwndSum, cwndSamples uint64
}

func (s *renoSender) run(ctx context.Context, recvDone <-chan int) (*RenoResult, error) {
	s.retransmitted = make(map[uint32]bool)

	acks := make(chan uint32, 1024)
	readCtx, stopReader := context.WithCancel(ctx)
	defer stopReader()
	go func() {
		buf := make([]byte, 2048)
		for {
			if readCtx.Err() != nil {
				return
			}
			n, _, err := s.ep.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < renoHeaderBytes || buf[4] != renoFlagAck {
				continue
			}
			select {
			case acks <- binary.BigEndian.Uint32(buf[:4]):
			default:
			}
		}
	}()

	start := time.Now()
	payload := make([]byte, s.mss)

	timer := time.NewTimer(s.rto)
	defer timer.Stop()

	for s.highestAck < uint32(s.total) {
		// Send whatever the window allows.
		for s.inFlight+uint32(s.mss) <= s.cwnd && s.sent < s.total {
			size := s.mss
			if remaining := s.total - s.sent; remaining < size {
				size = remaining
			}
			s.transmit(s.nextSeq, payload[:size], false)
			s.nextSeq += uint32(size)
			s.sent += size
			s.inFlight += uint32(size)
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(s.rto)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case ack := <-acks:
			s.onAck(ack)
		case <-timer.C:
			s.onTimeout()
		case n := <-recvDone:
			s.highestAck = uint32(n)
		}
	}

	// Tell the receiver we are finished, so it can report.
	fin := make([]byte, renoHeaderBytes)
	fin[4] = renoFlagFin
	_, _ = s.ep.WriteTo(fin, s.dst)

	elapsed := time.Since(start)
	res := s.stats
	res.BytesTransferred = s.total
	res.Elapsed = elapsed
	res.Goodput = Throughput{Bytes: uint64(s.total), Duration: elapsed}
	if s.cwndSamples > 0 {
		res.CwndMeanBytes = s.cwndSum / s.cwndSamples
	}
	return &res, nil
}

func (s *renoSender) transmit(seq uint32, body []byte, isRetransmit bool) {
	pkt := make([]byte, renoHeaderBytes+len(body))
	binary.BigEndian.PutUint32(pkt[:4], seq)
	pkt[4] = renoFlagData
	copy(pkt[renoHeaderBytes:], body)
	_, _ = s.ep.WriteTo(pkt, s.dst)

	s.stats.PacketsSent++
	if isRetransmit {
		s.stats.Retransmits++
		s.retransmitted[seq] = true
	} else {
		s.inflight[seq] = time.Now()
	}
}

func (s *renoSender) onAck(ack uint32) {
	if s.cwnd > s.stats.CwndMaxBytes {
		s.stats.CwndMaxBytes = s.cwnd
	}
	s.cwndSum += uint64(s.cwnd)
	s.cwndSamples++

	if ack <= s.highestAck {
		// A duplicate acknowledgement: the receiver got something out of
		// order, so something ahead of it was lost.
		s.dupAcks++
		if s.dupAcks == renoDupAcksBeforeRetransmit {
			// Fast retransmit and fast recovery: halve the window and resend
			// the segment the receiver is waiting for.
			s.ssthresh = maxU32(s.cwnd/2, uint32(2*s.mss))
			s.cwnd = s.ssthresh
			s.stats.FastRetransmits++
			s.resendFrom(s.highestAck)
		}
		return
	}

	// New data acknowledged.
	acked := ack - s.highestAck
	s.dupAcks = 0

	if sentAt, ok := s.inflight[s.highestAck]; ok && !s.retransmitted[s.highestAck] {
		s.sampleRTT(time.Since(sentAt))
	}
	for seq := range s.inflight {
		if seq < ack {
			delete(s.inflight, seq)
			delete(s.retransmitted, seq)
		}
	}

	s.highestAck = ack
	if s.inFlight > acked {
		s.inFlight -= acked
	} else {
		s.inFlight = 0
	}

	if s.cwnd < s.ssthresh {
		// Slow start: one segment per acknowledged segment, so the window
		// doubles each round trip.
		s.cwnd += acked
	} else {
		// Congestion avoidance: one segment per round trip.
		s.cwnd += uint32(float64(s.mss) * float64(s.mss) / float64(s.cwnd))
	}
}

// resendFrom retransmits the segment the receiver is waiting for.
func (s *renoSender) resendFrom(seq uint32) {
	size := s.mss
	if remaining := s.total - int(seq); remaining < size {
		size = remaining
	}
	if size <= 0 {
		return
	}
	s.transmit(seq, make([]byte, size), true)
}

func (s *renoSender) onTimeout() {
	s.stats.Timeouts++
	// RFC 5681: collapse to one segment, halve the threshold, back off the
	// timer.
	s.ssthresh = maxU32(s.cwnd/2, uint32(2*s.mss))
	s.cwnd = uint32(s.mss)
	s.dupAcks = 0
	s.rto *= 2
	if s.rto > renoMaxRTO {
		s.rto = renoMaxRTO
	}
	// Everything unacknowledged is presumed lost; go back to the last
	// acknowledged byte.
	s.nextSeq = s.highestAck
	s.sent = int(s.highestAck)
	s.inFlight = 0
	s.resendFrom(s.highestAck)
}

// sampleRTT is RFC 6298's estimator.
func (s *renoSender) sampleRTT(sample time.Duration) {
	if s.srtt == 0 {
		s.srtt = sample
		s.rttvar = sample / 2
	} else {
		diff := s.srtt - sample
		if diff < 0 {
			diff = -diff
		}
		s.rttvar = (3*s.rttvar + diff) / 4
		s.srtt = (7*s.srtt + sample) / 8
	}
	s.rto = s.srtt + 4*s.rttvar
	if s.rto < renoMinRTO {
		s.rto = renoMinRTO
	}
	if s.rto > renoMaxRTO {
		s.rto = renoMaxRTO
	}
}

// renoReceiver acknowledges every packet with the highest contiguous byte
// received, and reports the total when the sender says it is done.
func renoReceiver(ctx context.Context, ep *Endpoint, sender utp.ConnectionPeer, done chan<- int) {
	buf := make([]byte, 2048)
	var expected uint32
	outOfOrder := make(map[uint32]int)

	for {
		if ctx.Err() != nil {
			return
		}
		n, _, err := ep.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, ErrClosed) {
				return
			}
			continue
		}
		if n < renoHeaderBytes {
			continue
		}
		seq := binary.BigEndian.Uint32(buf[:4])
		switch buf[4] {
		case renoFlagFin:
			select {
			case done <- int(expected):
			default:
			}
			return
		case renoFlagData:
			body := n - renoHeaderBytes
			if seq == expected {
				expected += uint32(body)
				// Absorb anything that arrived early and is now in order.
				for {
					size, ok := outOfOrder[expected]
					if !ok {
						break
					}
					delete(outOfOrder, expected)
					expected += uint32(size)
				}
			} else if seq > expected {
				outOfOrder[seq] = body
			}
			ack := make([]byte, renoHeaderBytes)
			binary.BigEndian.PutUint32(ack[:4], expected)
			ack[4] = renoFlagAck
			_, _ = ep.WriteTo(ack, sender)
		}
	}
}

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
