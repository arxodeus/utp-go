package utp

import (
	"log/slog"
	"time"
)

// scheduleIdleRto arms the idle wake-up when nothing is outstanding, so the
// retransmission deadline is still honored on a quiet connection. While
// packets are in flight their own timers cover it.
func (c *connection) scheduleIdleRto(now time.Time) {
	if c.armIdleRto == nil || c.state.stateType != connConnected ||
		c.state.SentPackets == nil || len(c.armed) != 0 || c.rtoDeadline.IsZero() {
		return
	}
	d := c.rtoDeadline.Sub(now)
	if d < time.Millisecond {
		d = time.Millisecond
	}
	c.armIdleRto(d)
}

// onIdleRto handles the retransmission deadline passing with nothing in
// flight: the window decays by a third rather than collapsing, and the
// deadline is re-armed. The decay is the controller's; sentPackets.OnTimeout
// distinguishes this case by HasUnackedPackets being false.
func (c *connection) onIdleRto(now time.Time) {
	if c.state.stateType != connConnected || c.state.SentPackets == nil ||
		len(c.armed) != 0 || c.rtoDeadline.IsZero() || now.Before(c.rtoDeadline) {
		return
	}
	c.state.SentPackets.OnTimeout()
	// Re-arm from the doubled timeout OnTimeout has just applied.
	c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
	if c.logger.Enabled(baseContext, slog.LevelDebug) {
		stats := c.state.SentPackets.controllerStats()
		c.logger.Debug("idle window decay", "cwnd", stats.MaxWindowSizeBytes,
			"timeout", stats.Timeout)
	}
}

// freshlyStamped returns ack restamped as if built now in reply to in: with
// the current time and the delay measured from in. An acknowledgement kept
// to be resent, such as a FIN's, would otherwise carry stale stamps and feed
// an old sample to the peer's delay estimate. A zero timestamp in in means
// no delay sample, as on a live connection (onPacket).
func freshlyStamped(ack *packet, now uint32, in *packet) *packet {
	out := *ack
	header := *ack.Header
	header.Timestamp = int64(now)
	header.TimestampDiff = 0
	if peer := uint32(in.Header.Timestamp); peer != 0 {
		header.TimestampDiff = uint32(timestampDiffMicros(now, peer).Microseconds())
	}
	out.Header = &header
	return &out
}

// lingerAck returns the acknowledgement the socket should resend if the peer
// keeps retransmitting its FIN after this connection is gone.
//
// After acknowledging the peer's FIN and tearing down, a lost
// acknowledgement makes the peer retransmit the FIN; without this the socket,
// no longer knowing the connection, would answer with a RESET and turn a
// complete transfer into a failure for the peer.
func (c *connection) lingerAck() *packet {
	// Only a connection that ended gracefully, with a FIN sent, received or
	// both, lingers. One ended by a RESET, idle timeout or error still
	// draws a RESET for anything that arrives afterwards.
	if c.state.closing == nil {
		return nil
	}
	ack := c.finAck
	if ack == nil {
		// We sent a FIN but never received one, so there is no FIN
		// acknowledgement to repeat. The last acknowledgement sent serves:
		// its ack number distinguishes a retransmission from data sent past
		// our FIN.
		ack = c.lastStateSent
	}
	// Number it past our FIN, as every packet after the FIN is. Both
	// sources may be older, and a peer that has our FIN would drop an
	// acknowledgement numbered at or before it as stale.
	if ack != nil && c.state.closing.LocalFin != nil {
		next := *c.state.closing.LocalFin + 1
		if ack.Header.SeqNum != next {
			renumbered := *ack
			header := *ack.Header
			header.SeqNum = next
			renumbered.Header = &header
			ack = &renumbered
		}
	}
	return ack
}

// rememberLingerState captures an acknowledgement for the socket to repeat
// after the connection is gone, while there is still live state to build one
// from. It is called as the local FIN goes out: by shutdown statePacket has
// nothing left to build from, and an initiator that only sent data has no
// earlier STATE packet to fall back on.
func (c *connection) rememberLingerState() {
	if c.lastStateSent != nil {
		return
	}
	if pkt := c.statePacket(); pkt != nil {
		c.lastStateSent = pkt
	}
}

// notifySocketShutdown asks the socket to forget this connection. The wait is
// bounded because the socket's event loop may already be gone; a closing
// socket drops its whole table anyway.
func (c *connection) notifySocketShutdown() {
	if c.out != nil {
		c.out(newShutdownSocketEvent(c.cid, c.lingerAck()))
		return
	}
	timer := c.timeSource().NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case c.socketEvents <- newShutdownSocketEvent(c.cid, c.lingerAck()):
	case <-timer.C():
	}
}

// finFits reports whether the FIN may be sent now. The FIN is queued like a
// data packet and goes only when the window has room for a full packet, and
// after anything owed a resend. A FIN held back here is sent by a later pass
// of the event loop.
func (c *connection) finFits(now time.Time) bool {
	sp := c.state.SentPackets
	if sp == nil {
		return false
	}
	if _, owed := sp.NextNeedingResend(); owed {
		return false
	}
	if int(sp.UnackedCount()) >= maxOutstandingPackets {
		sp.OnWindowFull(now)
		return false
	}
	maxSend := minUint32(sp.CongestionWindow(), c.effectivePeerWindow(now))
	if uint64(sp.BytesInFlight())+uint64(c.mtu.payloadSize()) > uint64(maxSend) {
		// Record that the window is full.
		sp.OnWindowFull(now)
		return false
	}
	return true
}

func (c *connection) shutdown() {
	switch c.state.stateType {
	case connConnecting:
		c.state.stateType = connClosed
	case connClosed:
		// ignore
	case connConnected:
		if c.state.closing != nil {
			localFin := c.state.closing.LocalFin
			// Send our FIN once there are no pending writes and nothing left in
			// the send buffer.
			if localFin == nil && len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() && c.finFits(c.now()) {
				recvWindow := c.recvWindow()
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()

				// No selective ack: see statePacket.
				fin := newPacketBuilder(
					st_fin,
					c.cid.Send,
					c.nowMicros(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).Build()

				c.state.closing.LocalFin = &seqNum
				if c.logger.Enabled(baseContext, LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.rememberLingerState()
				c.transmit(fin, c.now(), true)
			}
		} else {
			var localFin *uint16
			if len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() && c.finFits(c.now()) {
				recvWindow := c.recvWindow()
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()

				// No selective ack: see statePacket.
				fin := newPacketBuilder(
					st_fin,
					c.cid.Send,
					c.nowMicros(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).
					Build()

				localFin = &seqNum
				if c.logger.Enabled(baseContext, LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.rememberLingerState()
				c.transmit(fin, c.now(), true)
			}
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("init localFin of closingRecord", "dst.peer", c.cid.Peer, "dst.send", c.cid.Send, "dst.recv", c.cid.Recv, "localFin", localFin)
			}
			c.state.closing = &closingRecord{
				LocalFin:  localFin,
				RemoteFin: nil,
			}
		}
	}
}

// updateClosingState decides whether the connection has finished. It runs
// both when a packet arrives and on every pass of the event loop: the side
// that closes second sends its FIN to a peer that may already be gone, so no
// packet may arrive to trigger the check.
func (c *connection) updateClosingState() {
	// Closing cases.
	if c.state.stateType == connConnected && c.state.closing != nil && c.state.closing.LocalFin != nil {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("try close connection locally...", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv)
		}
		lastAckNum, isNone := c.state.SentPackets.LastAckNum()
		// Only when the application closed entirely. A FIN sent by
		// CloseWrite says nothing about the reading side.
		if !isNone && lastAckNum == *c.state.closing.LocalFin && c.closeRequested {
			c.state.stateType = connClosed
			c.state.Err = nil
		}
	}

	// A connection whose peer has closed its sending side is not finished:
	// the reader gets its end-of-stream marker from processReads once the
	// receive buffer reaches the FIN, and the writer carries on.
	//
	// Once the application has closed, this end is finished when everything
	// it sent, its FIN included, has been acknowledged and the reader has
	// everything up to the peer's FIN. A peer that closed fully is gone by
	// then and answers our FIN with a RESET, which also ends the connection.
	//
	// LocalFin != nil matters: shutdown sends the FIN only once the send
	// buffer and pending writes have drained, so it proves nothing is left
	// to send. Without it, "nothing unacknowledged" would be true at every
	// momentary gap between packets.
	if c.state.stateType == connConnected && c.state.closing != nil &&
		c.state.closing.RemoteFin != nil && c.state.closing.LocalFin != nil &&
		c.closeRequested {
		if !c.state.SentPackets.HasUnackedPackets() &&
			c.state.RecvBuf.AckNum() == *c.state.closing.RemoteFin {
			c.processReads()
			c.state.stateType = connClosed
			c.state.Err = nil
		}
	}
}

func (c *connection) onReset() {
	// Record how much had arrived: a RESET before the peer's FIN cuts the
	// stream short, which readEndErr distinguishes from the routine RESET
	// after a peer closes first.
	var remoteFin, localFin any
	if cl := c.state.closing; cl != nil {
		if cl.RemoteFin != nil {
			remoteFin = *cl.RemoteFin
		}
		if cl.LocalFin != nil {
			localFin = *cl.LocalFin
		}
	}
	var ackNum any
	if c.state.RecvBuf != nil {
		ackNum = c.state.RecvBuf.AckNum()
	}
	c.logger.Warn("RESET from remote", "cid.send", c.cid.Send, "cid.recv", c.cid.Recv,
		"ackNum", ackNum, "remoteFin", remoteFin, "localFin", localFin,
		"receivedThroughFin", c.receivedThroughFin())

	if c.state.stateType == connClosed {
		return
	}
	// A reset answering our SYN is a refusal: nothing on the other end will
	// take the connection.
	err := ErrReset
	if c.state.stateType == connConnecting && c.endpoint.Type == initiator {
		err = ErrConnRefused
	}
	c.reset(err)
}

// onCloseRead shuts down the receiving side: from here on, arriving data is
// acknowledged and dropped rather than delivered. See UtpStream.CloseRead.
func (c *connection) onCloseRead() {
	if c.readShutdown {
		return
	}
	c.readShutdown = true
	c.logger.Debug("read side closed; incoming data will be acknowledged and discarded",
		"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)

	// Discard what is already buffered: the reader will not come back for
	// it, and holding it would keep the advertised window closed by that
	// much.
	if c.state.RecvBuf != nil {
		discarded := c.state.RecvBuf.Readable()
		if discarded > 0 {
			buf := make([]byte, discarded)
			c.state.RecvBuf.Read(buf)
		}
	}
}

func (c *connection) reset(err error) {
	c.logger.Warn("resetting connection", "err", err)

	// If we already sent our FIN and got a reset, assume the peer received
	// the FIN and closed, and treat this as a successful close.
	if c.state.stateType == connConnected {
		if c.state.closing != nil && c.state.closing.LocalFin != nil {
			c.state.stateType = connClosed
			return
		}
	}

	// A Connect still waiting for the handshake learns why it failed. Only
	// during the handshake: afterwards nobody waits on the channel, and its
	// one slot may still hold the success already sent.
	if c.state.stateType == connConnecting && c.state.connectedCh != nil {
		select {
		case c.state.connectedCh <- err:
		default:
		}
		c.state.connectedCh = nil
	}
	c.state.stateType = connClosed
	c.state.Err = err
}
