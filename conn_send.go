package utp

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type queuedWrite struct {
	data     []byte
	written  int
	resultCh chan *readOrWriteResult

	// mu orders the connection taking bytes from this write against its
	// writer giving up on it. written is the loop's, read by the writer
	// under mu; canceled is the writer's, read by the loop under mu.
	mu       sync.Mutex
	canceled bool
}

// cancel withdraws what the connection has not yet taken and reports what it
// has: the number of bytes already in the send buffer, which will be sent, or
// the result if the whole write was taken before the writer gave up.
func (w *queuedWrite) cancel() (int, *readOrWriteResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.canceled = true
	select {
	case res := <-w.resultCh:
		return 0, res
	default:
	}
	return w.written, nil
}

// emit hands a packet to the socket and records when this connection last
// sent, so the keep-alive can tell whether it has been silent.
func (c *connection) emit(pkt *packet) {
	c.emitPacket(pkt, false)
}

// emitPacket sends a packet through the socket. dontFragment marks it as an
// MTU probe; see DontFragmentWriter.
func (c *connection) emitPacket(pkt *packet, dontFragment bool) {
	if pkt == nil {
		return
	}
	c.lastSentPacket = c.now()
	if pkt.Header != nil {
		c.lastAdvertisedWindow = pkt.Header.WndSize
	}
	if pkt.Header.PacketType == stState {
		// Kept for the socket to resend after this connection is gone.
		c.lastStateSent = pkt
	}
	if c.out != nil {
		// Send directly on this goroutine, so nothing is in flight between
		// deciding to send and sending. The event is finished with when out
		// returns, so one serves every packet.
		c.outEvent = socketEvent{Type: outgoing, Packet: pkt, ConnectionID: c.cid, DontFragment: dontFragment}
		c.out(&c.outEvent)
		c.outEvent = socketEvent{}
		return
	}
	ev := newOutgoingSocketEvent(pkt, c.cid)
	if dontFragment {
		ev = newOutgoingProbeSocketEvent(pkt, c.cid)
	}
	// A virtual clock must not consider the system quiet while this packet
	// is queued but not yet taken from socketEvents. See
	// idleBarrier.NoteHandoff.
	if b, ok := c.timeSource().(idleBarrier); ok {
		b.NoteHandoff()
	}
	c.socketEvents <- ev
}

// keepAlivePacket returns a STATE packet acknowledging one less than we have.
// The peer takes it for a stale acknowledgement and answers, so the exchange
// proves both directions work without consuming a sequence number.
func (c *connection) keepAlivePacket() *packet {
	pkt := c.statePacket()
	if pkt == nil {
		return nil
	}
	pkt.Header.AckNum-- // wrapping
	// Shift the selective ack to match the decremented ack number: bit i
	// now means ack_nr+1+i. The new bit 0 is ack_nr+1, the packet a
	// selective ack exists because we lack, and the top bit falls out of the
	// window. Unshifted, the peer would read every bit one packet early.
	if pkt.Eack != nil {
		old := pkt.Eack.Acked()[:selectiveAckWindow]
		shifted := make([]bool, len(old))
		copy(shifted[1:], old[:len(old)-1])
		pkt.Eack = newSelectiveAck(shifted)
	}
	return pkt
}

// keepAliveIntervalOrDefault returns the configured interval or the default.
func (c *connection) keepAliveIntervalOrDefault() time.Duration {
	if c.config.KeepAliveInterval > 0 {
		return c.config.KeepAliveInterval
	}
	return defaultKeepAliveInterval
}

// zeroWindowProbeIntervalOrDefault returns the configured interval or the
// default.
func (c *connection) zeroWindowProbeIntervalOrDefault() time.Duration {
	if c.config.ZeroWindowProbeInterval > 0 {
		return c.config.ZeroWindowProbeInterval
	}
	return defaultZeroWindowProbeInterval
}

// effectivePeerWindow returns how much the peer says it can accept, with the
// zero-window probe applied: once the probe interval has passed, one packet
// is let through a closed window, and the peer's answer carries a fresh
// window.
func (c *connection) effectivePeerWindow(now time.Time) uint32 {
	if c.peerRecvWindow > 0 {
		return c.peerRecvWindow
	}
	if c.zeroWindowProbeDue.IsZero() || now.Before(c.zeroWindowProbeDue) {
		return 0
	}
	c.probingZeroWindow = true
	return uint32(c.config.MaxPacketSize)
}

func (c *connection) processWrites(now time.Time) {
	if c.state.SentPackets != nil {
		// LEDBAT++'s slowdowns are driven by the clock, not by acks; otherwise
		// a connection whose application goes quiet mid-slowdown would stay
		// pinned at two packets until an ack arrived.
		c.state.SentPackets.OnTick(now)
	}
	switch c.state.stateType {
	case connConnecting:
		return
	case connClosed:
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Warn("connection is closed, will not process pending writes",
				"c.cid.send", c.cid.Send, "c.cid.recv", c.cid.Recv)
		}
		// A writer still waiting has not had all its bytes taken, so it is
		// told why (ErrNotConnected for a clean close, which has no error of
		// its own) and how many bytes were taken before the end.
		err := c.state.Err
		if err == nil {
			err = ErrNotConnected
		}
		// Answer each writer once: a result channel holds one value, and a
		// second send would block with the connection's lock held.
		for _, w := range c.pendingWrites {
			w.resultCh <- &readOrWriteResult{Len: w.written, Err: err}
		}
		c.pendingWrites = nil
		return
	default:
	}

	// Compose data packets.
	//
	// A packet is sent only while the window has room for a full packet,
	// counting a full packet whatever the next one holds:
	//
	//   - Bytes in flight count against the peer's window as well as the
	//     congestion window. The peer's advertisement is how much it can
	//     take beyond what it has acknowledged; everything sent since spends
	//     it.
	//   - Nothing is sent into less than a full packet of room, and a packet
	//     is never cut down to fit.
	//
	// The send buffer bounds what the application may queue, not what may
	// be in flight.
	packetSize := c.mtu.payloadSize()
	maxSend := minUint32(c.state.SentPackets.CongestionWindow(), c.effectivePeerWindow(now))
	// Never more than maxOutstandingPackets in flight, however small: a run
	// of small writes into a large window could otherwise put more packets
	// in flight than a receiver will reorder, or than 16-bit sequence
	// numbers can order.
	outstanding := int(c.state.SentPackets.UnackedCount())

	// Packets a retransmission timeout declared lost go first, oldest
	// first, under the same rule as new data, so a resend always precedes
	// the new data behind it.
	resendsWaiting := false
	for {
		pkt, ok := c.state.SentPackets.NextNeedingResend()
		if !ok {
			break
		}
		if outstanding >= maxOutstandingPackets ||
			uint64(c.state.SentPackets.BytesInFlight())+uint64(packetSize) > uint64(maxSend) {
			// No room even for what is owed; new data waits behind it.
			resendsWaiting = true
			break
		}
		c.resendSentPacket(pkt, now)
	}
	inFlight := c.state.SentPackets.BytesInFlight()
	// Take the slice rather than share it.
	payloads := c.payloadScratch[:0]
	c.payloadScratch = nil
	var composed uint32

	// Record whether the application is filling the window. The congestion
	// controller does not grow a window the application never fills; the
	// signal is "we had data and no room for it".
	windowFull := resendsWaiting

	for !resendsWaiting && c.state.SendBuf.Pending() > 0 {
		if outstanding+len(payloads) >= maxOutstandingPackets ||
			uint64(inFlight)+uint64(composed)+uint64(packetSize) > uint64(maxSend) {
			windowFull = true
			break
		}
		n := minUint32(uint32(c.state.SendBuf.Pending()), packetSize)
		// Nagle: the last packet waits while it is short of a full one and
		// anything is ahead of it in the window, sent and unacknowledged or
		// composed in this pass, and the next write joins it. Every
		// acknowledgement brings this loop round again, so the one that
		// empties the window releases it.
		if n < packetSize && !c.config.NoDelay && !c.writeShut && outstanding+len(payloads) > 0 {
			break
		}
		data := c.state.SendBuf.Take(int(n))
		n = uint32(len(data))
		payloads = append(payloads, data)
		composed += n
	}
	if windowFull {
		c.state.SentPackets.OnWindowFull(now)
	}

	// Move pending writes into the send buffer.
	for len(c.pendingWrites) > 0 {
		writeReq := c.pendingWrites[0]
		writeReq.mu.Lock()
		// A write whose caller gave up is dropped whether or not there is
		// room, so it does not hold a FIN behind it.
		if writeReq.canceled {
			writeReq.mu.Unlock()
			c.pendingWrites[0] = nil
			c.pendingWrites = c.pendingWrites[1:]
			continue
		}
		bufSpace := c.state.SendBuf.Available()
		if bufSpace <= 0 {
			writeReq.mu.Unlock()
			break
		}

		// The queued data is the stream's own copy (writeQueued), so the send
		// buffer adopts it.
		if len(writeReq.data) <= bufSpace {
			c.state.SendBuf.Adopt(writeReq.data)
			result := &readOrWriteResult{
				Len: len(writeReq.data) + writeReq.written,
			}
			writeReq.resultCh <- result
			c.pendingWrites[0] = nil
			c.pendingWrites = c.pendingWrites[1:]
		} else {
			nextWrite := writeReq.data[:bufSpace]
			remainingData := writeReq.data[bufSpace:]
			c.state.SendBuf.Adopt(nextWrite)

			writeReq.data = remainingData
			writeReq.written += bufSpace
		}
		writeReq.mu.Unlock()
		c.wantWrite = true
	}

	// Transmit data packets.
	seqNum := c.state.SentPackets.NextSeqNum()
	recvWindow := c.recvWindow()
	ackNum := c.state.RecvBuf.AckNum()

	for _, payload := range payloads {
		// No selective ack: see statePacket.
		packetInst := newPacketBuilder(
			stData,
			c.cid.Send,
			c.nowMicros(),
			recvWindow,
			seqNum,
		).WithPayload(payload).WithTimestampDiffMicros(uint32(c.peerTimestampDiff.Microseconds())).WithAckNum(ackNum).Build()

		c.transmit(packetInst, now, true)
		seqNum = seqNum + 1 // wrapping add
	}
	clear(payloads)
	c.payloadScratch = payloads[:0]
}

func (c *connection) onWrite(writeReq *queuedWrite) {
	switch c.state.stateType {
	case connConnecting:
		// No bytes written so far.
		c.pendingWrites = append(c.pendingWrites, writeReq)

	case connConnected:
		if c.state.closing != nil {
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("append a queuedWrite to pending writes when closing the conn...")
			}
			if c.state.closing.LocalFin == nil && c.state.closing.RemoteFin != nil {
				c.pendingWrites = append(c.pendingWrites, writeReq)
			} else {
				// Our FIN is out, so nothing more may be sent. Report an error
				// rather than a zero-length success.
				writeReq.resultCh <- &readOrWriteResult{Err: ErrNotConnected}
			}
		} else {
			c.logger.Debug("append a queuedWrite to pending writes")
			c.pendingWrites = append(c.pendingWrites, writeReq)
		}

	case connClosed:
		c.logger.Warn("discard a queuedWrite when closed the conn...")
		// A connection that closed cleanly has no error of its own, and a
		// write that sends nothing must still report one.
		err := c.state.Err
		if err == nil {
			err = ErrNotConnected
		}
		writeReq.resultCh <- &readOrWriteResult{Err: err}
	}
	c.processWrites(c.now())
	c.wantWrite = true
}

// maxOutstandingPackets is how many packets may be in flight at once. See
// processWrites.
const maxOutstandingPackets = 1024 - 1

// defaultZeroWindowProbeInterval is how long a closed peer window is
// tolerated before a packet is forced through.
const defaultZeroWindowProbeInterval = 15 * time.Second

// defaultKeepAliveInterval is how long an established connection may stay
// silent before sending a keep-alive. 29 seconds is below the 30-second UDP
// mapping timeout common in NATs; a connection quiet for longer loses its
// mapping and cannot be reached from outside.
const defaultKeepAliveInterval = 29 * time.Second

func (c *connection) synPacket(seqNum uint16) *packet {
	nowMicros := int64(c.nowMicros())
	return newPacketBuilder(
		stSyn,
		c.cid.Recv,
		uint32(nowMicros),
		c.config.WindowSize,
		seqNum,
	).Build()
}

// transmit sends a packet and arms its retransmission timer.
// firstTransmission distinguishes a first transmission from a resend; only
// the former can be an MTU probe.
func (c *connection) transmit(packet *packet, now time.Time, firstTransmission bool) {
	if c.state.stateType == connClosed && errors.Is(c.state.Err, errInternal) {
		// Ended by an earlier failure, with packets of this pass still to go.
		return
	}
	var payload []byte
	var length uint32

	if len(packet.Body) > 0 {
		payload = packet.Body
		length = uint32(len(packet.Body))
	}

	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("will transmit packet",
			"cid", packet.Header.ConnectionID,
			"packet.type", packet.Header.PacketType.String(),
			"packet.seqNum", packet.Header.SeqNum,
			"packet.ackNum", packet.Header.AckNum,
			"innerMap.key", packet.Header.SeqNum,
			"packet.body.len", len(packet.Body))
	}

	c.packetsSent++
	c.bytesSent += uint64(len(packet.Body))
	if !firstTransmission {
		c.noteResend(packet)
		// A probe resent without don't-fragment may be fragmented, so its
		// acknowledgement no longer shows the size fits. Forget the probe
		// and let the search send another. The loss probe, which resends the
		// oldest outstanding packet, can pick the probe.
		if c.mtu.probeOutstanding(packet.Header.SeqNum) {
			c.mtu.clearProbe()
		}
	}

	// Use this packet as an MTU probe if the search wants one. The probe is
	// sent with fragmentation disabled if the Conn supports it. Otherwise it
	// is sent normally, and on IPv4 a router may fragment an oversized probe
	// rather than drop it, so the search can settle on a size that works
	// only through fragmentation.
	isProbe := false
	if datagramSize := uint32(packet.EncodedLen()); c.mtu.eligibleProbe(datagramSize, firstTransmission) {
		c.mtu.beginProbe(packet.Header.SeqNum, datagramSize)
		isProbe = true
		if c.logger.Enabled(baseContext, slog.LevelDebug) {
			c.logger.Debug("MTU probe", "size", datagramSize,
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "seq", packet.Header.SeqNum)
		}
	}

	if err := c.state.SentPackets.OnTransmit(packet.Header.SeqNum, packet.Header.PacketType, payload, length, now); err != nil {
		// This connection's accounting no longer matches what it is sending.
		// It ends with the reason. See sentPackets.OnTransmit.
		if isProbe {
			c.mtu.clearProbe()
		}
		c.logger.Error("send bookkeeping failed; ending the connection", "err", err,
			"seq", packet.Header.SeqNum, "cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
		c.state.stateType = connClosed
		c.state.Err = fmt.Errorf("%w: %w", errInternal, err)
		return
	}
	c.armRetransmit(packet, c.state.SentPackets.Timeout())

	// A packet carrying the current acknowledgement and selective ack says
	// everything a deferred ST_STATE would, so that is no longer owed.
	if c.state.RecvBuf != nil && packet.Header.AckNum == c.state.RecvBuf.AckNum() {
		c.ackPending = false
		c.ackHeldSince = time.Time{}
		c.dataSinceAck = 0
	}

	c.emitPacket(packet, isProbe)
}
