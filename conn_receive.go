package utp

import (
	"errors"
	"log/slog"
	"math"
	"time"
)

// reorderBufferMaxSize bounds how far past the next expected sequence number
// a packet may be and still be processed, and so how much pending state a
// peer can make the connection hold.
const reorderBufferMaxSize = uint16(1024)

// reorderOldPacketFloor is the other end of the same test: a distance at or
// above this has wrapped, so the packet is an old one at most
// reorderBufferMaxSize behind rather than one far in the future.
const reorderOldPacketFloor = uint16(1<<16 - 1024)

// outsideReorderWindow reports whether a packet's sequence number is too far
// from the expected one to process, and if so answers it.
//
// The distance counts how far past the next expected packet this one is, so
// 0 is the packet being waited for. Beyond reorderBufferMaxSize the packet is
// either far in the future or, by wrapping, an old one already consumed;
// either way it is dropped without buffering, acknowledging or affecting
// congestion control. An old packet that is not a STATE also triggers a
// fresh acknowledgement, since the peer evidently missed the last one.
//
// The check applies to ST_DATA, ST_STATE and ST_FIN. A SYN never reaches it,
// and a RESET is handled before it.
func (c *connection) outsideReorderWindow(pkt *packet) bool {
	switch pkt.Header.PacketType {
	case st_data, st_state, st_fin:
	default:
		return false
	}
	if c.state.stateType != connConnected || c.state.RecvBuf == nil {
		return false
	}

	distance := pkt.Header.SeqNum - c.state.RecvBuf.AckNum() - 1 // wrapping
	if distance < reorderBufferMaxSize {
		return false
	}

	if distance >= reorderOldPacketFloor && pkt.Header.PacketType != st_state {
		// An old packet the peer is still retransmitting: acknowledge it again
		// so the peer can stop. Emitted directly rather than through
		// transmit, since a STATE is neither numbered nor retransmitted.
		if statePkt := c.statePacket(); statePkt != nil {
			c.emit(statePkt)
		}
	}
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("dropping packet outside the reorder window",
			"seqNum", pkt.Header.SeqNum, "ackNum", c.state.RecvBuf.AckNum(),
			"distance", distance)
	}
	return true
}

func (c *connection) onPacket(packet *packet, now time.Time) {
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("on packet start...",
			"packet.body.len", len(packet.Body),
			"packet.type", packet.Header.PacketType.String(),
			"packet.cid", packet.Header.ConnectionId,
			"packet.seqnum", packet.Header.SeqNum,
			"packet.acknum", packet.Header.AckNum,
			"packet.windowSize", packet.Header.WndSize,
			"now", now)
	}
	// A SYN other than this connection's own retransmitted SYN is ignored,
	// and the connection is unaffected: it may be a peer's new connection
	// that drew the same ids as one this side still holds. A SYN with this
	// connection's own sequence number means the peer missed the SYN-ACK,
	// and is answered (the st_syn case below).
	if packet.Header.PacketType == st_syn && !c.isOwnSyn(packet.Header.SeqNum) {
		if c.logger.Enabled(baseContext, slog.LevelDebug) {
			c.logger.Debug("ignoring a SYN that is not this connection's",
				"seq", packet.Header.SeqNum, "cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
		}
		return
	}

	// Every packet from the peer shows the connection is alive, which Close
	// watches to decide whether waiting for the flush is still worthwhile.
	c.peerActivity.Add(1)
	c.dropUnacked = false
	c.packetsReceived++
	c.bytesReceived += uint64(len(packet.Body))

	// A RESET ends the connection whatever its numbers say. It must not go
	// through the checks below: a RESET acknowledges the packet that drew
	// it, which may be our next unsent sequence number, and its random
	// sequence number may fall outside the reorder window.
	if packet.Header.PacketType == st_reset {
		c.onReset()
		return
	}

	// Validate the acknowledgement number first. A packet rejected here
	// must not first draw a re-ack from the reorder-window check below.
	if c.invalidAckNum(packet) {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("dropping packet with an out-of-range ack number",
				"ackNum", packet.Header.AckNum, "type", packet.Header.PacketType.String())
		}
		return
	}

	// An extension-bits extension must be exactly 8 bytes, or the packet is
	// dropped. Not checked for a SYN, which does not reach here once the
	// connection exists.
	if packet.Header.PacketType != st_syn && packet.malformedExtensionBits() {
		return
	}

	if c.outsideReorderWindow(packet) {
		return
	}

	// Take the peer's window, and the zero-window probe, only from a packet
	// that passed both checks above, so a forged packet cannot close our
	// view of the peer's window.
	c.peerRecvWindow = packet.Header.WndSize
	// Restart the probe deadline on every packet reporting a zero window,
	// so the probe goes one interval after the last. The next packet
	// replaces the forced one-packet window with whatever the peer reports.
	if c.peerRecvWindow == 0 {
		interval := c.zeroWindowProbeIntervalOrDefault()
		c.zeroWindowProbeDue = now.Add(interval)
		if c.armProbeTimer != nil {
			c.armProbeTimer(interval)
		}
	} else {
		c.zeroWindowProbeDue = time.Time{}
	}
	c.probingZeroWindow = false

	// Measure how long ago the peer stamped this packet. That value is
	// echoed back in timestamp_difference_microseconds and is the peer's
	// delay signal for congestion control.
	//
	// It is measured only after the validation above, so a packet bad
	// enough to be dropped cannot change the peer's delay signal. It is not
	// capped: a peer whose clock is ahead produces a value that wraps, and
	// the receiving end handles it. A zero timestamp means no timestamp and
	// is neither echoed as a delay nor sampled.
	peerStamp := uint32(packet.Header.Timestamp)
	// Read the microsecond clock once for both uses below.
	var nowMicros uint32
	if peerStamp == 0 {
		c.peerTsDiff = 0
	} else {
		nowMicros = c.nowMicros()
		c.peerTsDiff = timestampDiffMicros(nowMicros, peerStamp)
	}
	// The delay in the peer's direction is not a congestion signal for this
	// sender, but it reveals clock drift between the ends. The raw wrapping
	// value goes to the controller: a slow clock makes this measurement
	// shrink until it wraps, and capping it would hide exactly that. See
	// peerDelayHist.
	if c.state != nil && c.state.SentPackets != nil && peerStamp != 0 {
		c.state.SentPackets.OnPeerDelay(
			wrappingSubUint32(nowMicros, peerStamp), now)
	}

	// Handle each packet type.
	var err error
	switch packet.Header.PacketType {
	case st_syn:
		c.onSyn(packet.Header.SeqNum)
	case st_state:
		c.onState(packet.Header.SeqNum, packet.Header.AckNum)
	case st_data:
		err = c.onData(packet.Header.SeqNum, packet.Body)
	case st_fin:
		err = c.onFin(packet.Header.SeqNum, packet.Body)
	case st_reset:
		c.onReset()
	}
	if err != nil {
		c.logger.Warn("on packet handle data or fin err", "err", err)
	}

	// Process acknowledgments.
	switch packet.Header.PacketType {
	case st_state, st_data, st_fin:
		// INT_MAX means the peer has no measurement, not a delay of 35 minutes.
		replyMicros := packet.Header.TimestampDiff
		if replyMicros == math.MaxInt32 {
			replyMicros = 0
		}
		delay := time.Duration(replyMicros) * time.Microsecond
		// The peer's timestamp less the delay it measured is the timestamp of
		// the last packet of ours it received, in our clock: the clocks'
		// offset cancels out. See probeAnswered.
		c.ackEchoKnown = replyMicros != 0
		c.ackEcho = uint32(packet.Header.Timestamp) - replyMicros
		// Before the acknowledgement retires anything: duplicates are counted
		// against the window as it stood when the packet arrived.
		c.noteDuplicateAck(packet.Header.PacketType, packet.Header.AckNum)
		err = c.processAck(packet.Header.AckNum, packet.Eack, delay, now)
		if err == nil && packet.Eack != nil {
			c.noteSelectiveAckCount(packet.Header.AckNum, packet.Eack)
		}
		c.onFastTimeout(now)
		c.rearmLossProbe(packet.Header.AckNum)
		if err != nil {
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("ack does not correspond to known seq_num",
					"packet.type", packet.Header.PacketType,
					"packet.seqNum", packet.Header.SeqNum,
					"packet.ackNum", packet.Header.AckNum,
					"err", err)
			}
		}
	default:
	}

	// Handle retransmissions.
	c.retransmitLostPackets(now)
	c.rearmRack()

	// Send a STATE packet if appropriate.
	switch packet.Header.PacketType {
	case st_syn:
		if c.synState == nil {
			// The SYN-ACK is generated at the start of the event loop.
			c.logger.Warn("missing SYN STATE")
			if statePacket := c.statePacket(); statePacket != nil {
				c.synState = statePacket
				c.emit(c.synState)
			} else {
				// Built as every reset is: no timestamps, no window, and the
				// sequence number of the packet that drew it.
				randSeqNum := randomUint16()
				resetPacket := newPacketBuilder(st_reset, packet.Header.ConnectionId, 0, 0, randSeqNum).
					WithAckNum(packet.Header.SeqNum).Build()
				c.emit(resetPacket)
			}
		} else {
			// A SYN for a connection we have already accepted is the initiator
			// retransmitting because our SYN-ACK was lost. Resend the same
			// SYN-ACK; nothing else retransmits it, so ignoring the SYN would
			// strand the peer.
			if c.logger.Enabled(baseContext, slog.LevelDebug) {
				c.logger.Debug("re-sending SYN-ACK for retransmitted SYN",
					"dst.peer", c.cid.Peer, "cid.send", c.cid.Send, "cid.recv", c.cid.Recv,
					"seqNum", c.synState.Header.SeqNum, "ackNum", c.synState.Header.AckNum)
			}
			c.emit(c.synState)
		}

	case st_fin:
		// Acknowledge a FIN immediately rather than deferring it: reaching a
		// FIN tears the connection down in the same pass, after which there
		// is nothing left to build the acknowledgement from.
		if c.dropUnacked {
			break
		}
		statePacket := c.statePacket()
		if statePacket == nil && c.state.stateType == connClosed {
			statePacket = c.closedFinAck(packet)
		}
		if statePacket != nil {
			// Kept so the socket can resend it if the peer retransmits this FIN
			// after the connection has gone (see lingerAck). It must be
			// captured now, for the same reason it cannot be deferred.
			c.finAck = statePacket
			c.emit(statePacket)
		}
	case st_data:
		// Record that an acknowledgement is owed; do not send it here. It is
		// sent once per socket read, so packets arriving together draw one
		// acknowledgement.
		if !c.dropUnacked {
			c.ackPending = true
			c.dataSinceAck++
			if !c.lastDataAt.IsZero() {
				gap := now.Sub(c.lastDataAt)
				c.lastDataGap = gap
				if c.dataGap == 0 {
					c.dataGap = gap
				} else {
					c.dataGap += (gap - c.dataGap) / 8
				}
				c.lastDataAt = now
			} else {
				c.lastDataAt = now
			}
		}
	}

	// Wake the writer on any packet that carries an acknowledgement, not
	// only ST_STATE: on a connection carrying data both ways the peer's
	// acknowledgements ride on its data packets, and a window they open
	// must be used.
	switch packet.Header.PacketType {
	case st_state, st_data, st_fin:
		c.wantWrite = true
	}

	// Data or a FIN is delivered by processReads, which every pass runs.

	c.updateClosingState()
}

// isOwnSyn reports whether a SYN is this connection's own, retransmitted by a
// peer that has not heard the SYN-ACK.
func (c *connection) isOwnSyn(seqNum uint16) bool {
	return c.endpoint.Type == acceptor && seqNum == c.endpoint.SynNum
}

// onSyn handles a SYN that reached the connection. Only the connection's own
// SYN does (see onPacket), and it is answered with the SYN-ACK again.
func (c *connection) onSyn(seqNum uint16) {}

func (c *connection) onState(seqNum, ackNum uint16) {
	if connConnecting != c.state.stateType {
		return
	}

	if c.endpoint.Type == initiator && ackNum == c.endpoint.SynNum {
		// Initialize the ack number to the SYN-ACK's sequence number minus 1,
		// as other implementations do, rather than as the specification
		// describes.
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("connect success, will initial connection state...",
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
		}
		recvBuf := newReceiveBufferWithLogger(c.config.BufferSize, seqNum-1, c.logger) // wrapping sub
		sendBuf := newSendBuffer(c.config.BufferSize)

		congestionCtrl := newDefaultController(c.controllerConfig())
		// The SYN's round trip is the first RTT sample, provided the SYN was
		// sent only once; otherwise the answer cannot be matched to one
		// transmission. It lets the first timeout be computed from the
		// handshake rather than the initial value.
		if c.endpoint.Attempts == 1 && !c.synSentAt.IsZero() {
			congestionCtrl.OnRTTSample(c.now().Sub(c.synSentAt))
		}
		sentPacketsHolder := newSentPackets(c.endpoint.SynNum, congestionCtrl, c.logger)
		sentPacketsHolder.rack.active = c.config.RACK

		// Report success by sending nil rather than closing the channel, which a
		// receiver checking ok would read as failure. The channel has room for
		// one, and the failure path only fires while still connecting, so this
		// cannot block or be followed by another send.
		if c.state.connectedCh != nil {
			c.state.connectedCh <- nil
			c.state.connectedCh = nil
		}
		c.state.stateType = connConnected
		c.state.RecvBuf = recvBuf
		c.state.SendBuf = sendBuf
		c.state.SentPackets = sentPacketsHolder
	}
}

func (c *connection) onData(seqNum uint16, data []byte) error {
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("on data packet", "seqNum", seqNum, "data.len", len(data))
	}
	// An empty payload is not an error: it is acknowledged and advances
	// the sequence space, delivering nothing.

	switch c.state.stateType {
	case connConnecting:
		if c.endpoint.Type == acceptor {
			return errors.New("unreachable: connection should be marked established")
		} else {
			// connection being established.
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("connection being established, ignore", "src.peer", c.cid.Peer, "seqNum", seqNum)
			}
			return nil
		}

	case connConnected:
		if c.state.closing != nil && c.state.closing.RemoteFin != nil {
			// connection is closing
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("connection has received remote FIN", "src.peer", c.cid.Peer, "seqNum", seqNum)
			}
			// Measure from the acknowledgement number rather than the first
			// sequence number, which stops being a lower bound once the
			// sequence space has wrapped.
			start := c.state.RecvBuf.AckNum()
			seqRange := newCircularRangeInclusive(start, *c.state.closing.RemoteFin)
			if !seqRange.Contains(seqNum) {
				// Numbered past the end of the stream: dropped without
				// acknowledgement, and the connection continues. The range
				// wraps, so a FIN numbered just past 65535 does not cut the
				// stream short.
				c.dropUnacked = true
				return nil
			}
		}
		// The application has stopped reading, so the bytes go nowhere, but
		// the sequence number must still advance or the peer would keep
		// retransmitting. A zero-length write records the sequence number
		// without copying or consuming space, so the window stays open and
		// out-of-order packets still fill their gaps.
		if c.readShutdown {
			data = nil
		}
		// An out-of-order packet already held is discarded without an
		// acknowledgement. An in-order duplicate never gets here; it is
		// behind the acknowledgement number, and outsideReorderWindow
		// re-acknowledges it.
		if seqNum != c.state.RecvBuf.AckNum()+1 && c.state.RecvBuf.HoldsOutOfOrder(seqNum) {
			c.dropUnacked = true
			return nil
		}
		// not closing; send data
		if c.state.RecvBuf.Admits(seqNum, len(data)) {
			err := c.state.RecvBuf.Write(data, seqNum)
			if err != nil {
				c.recvBufferDrops++
				c.logger.Warn("write data to recv buffer, but available space is not enough",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data))
			}
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("write data to recv buffer",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data), "nextAckNum", c.state.RecvBuf.AckNum())
			}
			return err
		}
		if !c.state.RecvBuf.WasWritten(seqNum) {
			c.recvBufferDrops++
		}
	default:
		// do nothing
	}
	return nil
}

// closedFinAck acknowledges a peer's FIN that arrived after this connection
// closed, or that closed it: a FIN that also acknowledged ours ends the
// connection in processAck before statePacket, which builds nothing for a
// closed connection, could answer it. It returns the linger acknowledgement,
// covering the FIN if it completes the stream, stamped now (freshlyStamped).
func (c *connection) closedFinAck(fin *packet) *packet {
	ack := c.lingerAck()
	if ack == nil || c.state.RecvBuf == nil {
		return nil
	}
	ackNum := c.state.RecvBuf.AckNum()
	if finSeq := fin.Header.SeqNum; finSeq == ackNum+1 {
		ackNum = finSeq
	}
	finAck := freshlyStamped(ack, c.nowMicros(), fin)
	finAck.Header.AckNum = ackNum
	return finAck
}

func (c *connection) onFin(seqNum uint16, data []byte) error {
	switch c.state.stateType {
	case connConnecting, connClosed:
		return nil

	case connConnected:
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("received FIN", "seq", seqNum, "c.cid.send", c.cid.Send, "c.cid.recv", c.cid.Recv)
		}
		// A FIN may carry a final payload, which is delivered like any other,
		// so a shut-down read side drops it the same way. Its sequence number
		// is still registered, since it marks the end of the stream.
		if c.readShutdown {
			data = nil
		}
		if c.state.closing != nil {
			if c.state.closing.RemoteFin != nil {
				// A FIN has already been received.
				if seqNum != *c.state.closing.RemoteFin {
					// The first FIN marks the end of the stream. A later one with a
					// different number is handled as data below.
					return c.onData(seqNum, data)
				}
			} else {
				remoteFin := seqNum
				c.state.closing.RemoteFin = &remoteFin
				return c.state.RecvBuf.Write(data, seqNum)
			}
		} else {
			// Register the FIN with the receive buffer.
			if err := c.state.RecvBuf.Write(data, seqNum); err != nil {
				return err
			}
			c.state.closing = &closingRecord{
				LocalFin:  nil,
				RemoteFin: &seqNum,
			}
		}
		return nil
	default:
		return nil
	}
}

// onICMP applies an ICMP report about a packet this connection sent. The
// socket finds the connection; this decides what the report means.
func (c *connection) onICMP(notice *icmpNotice, now time.Time) {
	if notice == nil {
		return
	}
	switch notice.Kind {
	case icmpFragmentationNeeded:
		c.onICMPFragmentationNeeded(notice.NextHopMTU, now)
	case icmpUnreachable:
		c.onICMPUnreachable()
	}
}

// onICMPFragmentationNeeded lowers the path-MTU ceiling on a router's report.
// See mtuSearch.icmpFragmentationNeeded.
func (c *connection) onICMPFragmentationNeeded(nextHopMTU uint32, now time.Time) {
	c.mtu.icmpFragmentationNeeded(nextHopMTU, now)
	// Log the router's figure.
	c.logger.Debug("MTU [ICMP]", "floor", c.mtu.floor, "ceiling", c.mtu.ceiling,
		"current", c.mtu.current, "nextHopMTU", nextHopMTU)
}

// onICMPUnreachable ends the connection after an ICMP error, with
// ErrConnRefused if only the SYN had been sent and ErrReset otherwise. A
// connection that has already finished is left alone.
func (c *connection) onICMPUnreachable() {
	if c.state.stateType == connClosed {
		return
	}

	err := ErrReset
	// Only the initiator, and only before the handshake completes, has
	// sent nothing but a SYN.
	if c.state.stateType == connConnecting && c.endpoint.Type == initiator {
		err = ErrConnRefused
	}

	c.logger.Warn("ICMP error, closing connection", "err", err,
		"closeRequested", c.closeRequested, "peer", c.cid.Peer)

	// Not c.reset(), which treats a reset after our own FIN as a clean
	// close: an ICMP error is reported whatever the state, since "the peer
	// went away" is what an application partway through a close needs to
	// hear. A connection still in the handshake has a Connect waiting on
	// this channel.
	if c.state.connectedCh != nil {
		c.state.connectedCh <- err
		c.state.connectedCh = nil
	}

	c.state.stateType = connClosed
	c.state.Err = err
}
