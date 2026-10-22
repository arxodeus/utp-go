package utp

import (
	"log/slog"
	"time"
)

// scheduleIdleRto arms the idle wake-up when there is nothing outstanding, so
// the retransmission deadline is still honoured on a connection that has gone
// quiet. It is a no-op while packets are in flight, where the per-packet
// timers already cover it.
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

// onIdleRto applies libutp's idle branch: the retransmission deadline passed
// with nothing in flight, so the window decays by a third rather than
// collapsing (utp_internal.cpp:1216-1222), and the deadline is set again.
//
// The decay itself is the controller's, reached by the same call the
// in-flight path uses: sentPackets.OnTimeout asks HasUnackedPackets, which is
// false here, which is exactly the distinction libutp draws with
// `cur_window_packets == 0`.
func (c *connection) onIdleRto(now time.Time) {
	if c.state.stateType != connConnected || c.state.SentPackets == nil ||
		len(c.armed) != 0 || c.rtoDeadline.IsZero() || now.Before(c.rtoDeadline) {
		return
	}
	c.state.SentPackets.OnTimeout()
	// libutp re-arms from the doubled timeout (:1204), which OnTimeout has
	// just applied.
	c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
	if c.logger.Enabled(baseContext, slog.LevelDebug) {
		stats := c.state.SentPackets.controllerStats()
		c.logger.Debug("idle window decay", "cwnd", stats.MaxWindowSizeBytes,
			"timeout", stats.Timeout)
	}
}

// lingerAck is the acknowledgement the socket should re-send if the peer keeps
// retransmitting its FIN after this connection is gone.
//
// A transfer can arrive whole and still end as a failure for the peer: we
// acknowledge the peer's FIN, hand the application its end of stream and tear
// down; if that acknowledgement is lost the peer retransmits the FIN, the
// socket no longer has the connection, and it would answer with a RESET.
// libutp cannot do this: it keeps the socket in CS_GOT_FIN until its own
// application closes, so a retransmitted FIN is simply acknowledged again
// (utp_internal.cpp:2369-2370).
//
// Only a connection that reached the peer's FIN has anything to linger for.
// Anything else -- a reset, an idle timeout, a local close with no FIN
// received -- should still draw a RESET, which is what libutp does for a
// packet it has no socket for. History: HISTORY.md, "lingerAck".
func (c *connection) lingerAck() *packet {
	// Only a connection that ended gracefully -- a FIN was sent, received, or
	// both -- has anything to linger for. One killed by a RESET, an idle
	// timeout or an error should still draw a RESET for whatever arrives
	// afterwards, which is what libutp does for a packet it has no socket for.
	if c.state.closing == nil {
		return nil
	}
	ack := c.finAck
	if ack == nil {
		// We sent the FIN and never received one, so there is no FIN
		// acknowledgement to repeat. The last acknowledgement we did send
		// serves: it tells a peer retransmitting what we already have, and its
		// ack number is what distinguishes a retransmission from data written
		// past our FIN.
		ack = c.lastStateSent
	}
	// Numbered past our FIN, as every packet after it is: libutp's send_ack
	// stamps seq_nr, the next to send (utp_internal.cpp:783). Both sources
	// can be older -- the last state is captured as the FIN goes out, the
	// FIN's acknowledgement may precede it -- and a peer that has our FIN
	// takes an acknowledgement numbered at or before it for an old packet
	// and drops it: re-acknowledging its FIN never ended it.
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
// after this connection is gone, while there is still live state to build one
// from.
//
// It is called as the local FIN goes out. By the time the connection actually
// shuts down, statePacket() has nothing left to build from -- the same reason
// the acknowledgement for a peer's FIN cannot be deferred -- and an initiator
// that only ever sent data has no earlier STATE to fall back on, because its
// acknowledgements ride on the data packets.
func (c *connection) rememberLingerState() {
	if c.lastStateSent != nil {
		return
	}
	if pkt := c.statePacket(); pkt != nil {
		c.lastStateSent = pkt
	}
}

// notifySocketShutdown asks the socket to forget this connection.
//
// The wait is bounded because the socket's event loop may already be gone --
// if the socket is closing, it drops its whole table anyway, so there is
// nothing left to clean up and blocking here would leak the very goroutine
// this is trying to tidy up after.
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

// finFits reports whether the FIN may go now.
//
// libutp queues its FIN in the outgoing buffer like any data packet
// (`write_outgoing_packet(0, ST_FIN, NULL, 0)`, utp_internal.cpp:3377, :3419) and
// flush_packets sends it only while !is_full(), whose default is room for a
// whole packet (:931-985) -- behind anything owed a resend, since the walk
// is oldest first. This sent the FIN as soon as the send buffer was empty,
// whatever the window said. With bytes in flight above a window a loss had
// just halved, the controller refused even the FIN's zero bytes, and
// transmit panicked: 1 run in 6 of fifteen two-way transfers at 5% loss
// took the process down. Shutdown runs on every pass of the event loop, so
// a FIN held back here goes on the pass that finds room.
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
		// libutp's is_full records this too (:945, :957).
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
			// If we have not sent our FIN, and there are no pending writes, and there is no
			// pending data in the send buffer, then send our FIN
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

// updateClosingState decides whether the connection has finished.
//
// It is called both when a packet arrives and once per pass of the event loop.
// Only the first of those used to happen, and the second is not decoration: the
// side that closes second sends its FIN into a peer whose connection is
// already gone, so nothing ever arrives to trigger the check again. Measured
// when it was packet-driven only -- 29 connections still tracked after 60
// closed cycles and 48 goroutines grown, every one of them that second closer.
func (c *connection) updateClosingState() {
	// Handle connection closing cases
	if c.state.stateType == connConnected && c.state.closing != nil && c.state.closing.LocalFin != nil {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("try close connection locally...", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv)
		}
		lastAckNum, isNone := c.state.SentPackets.LastAckNum()
		// Only when the application asked to close entirely. A FIN sent by
		// CloseWrite is acknowledged like any other packet and means nothing
		// about the reading side, which is still someone's (:2178-2182).
		if !isNone && lastAckNum == *c.state.closing.LocalFin && c.closeRequested {
			c.state.stateType = connClosed
			c.state.Err = nil
		}
	}

	// A connection whose peer has closed its sending side is *not* finished.
	//
	// libutp holds the socket in CS_GOT_FIN and keeps delivering
	// (utp_internal.cpp:2314 drops only what arrives past the FIN it has
	// reached), until its application closes too. So does this: the reader
	// gets its end-of-stream marker from processReads when the receive buffer
	// catches up to the FIN, and the writer carries on.
	//
	// Once the application *has* closed, this end is finished when everything
	// it sent, its FIN included, has been acknowledged and the reader has
	// everything up to the peer's FIN: libutp destroys a socket with
	// close_requested once its FIN is acknowledged (fin_sent_acked,
	// utp_internal.cpp:2174-2181). A peer that closed fully has gone by then,
	// and answers this FIN with a RESET, which ends this connection too.
	//
	// It used to end the moment both FINs existed as well, in a second block
	// with this one's conditions and none of its checks: whatever was still
	// unacknowledged -- the end of the data, and the FIN -- was abandoned,
	// and a peer that had only half-closed, still reading, never got it.
	// TestCloseAfterThePeersFinDeliversWhatWasWritten (netem); review of
	// 294cc2e.
	//
	// LocalFin != nil is doing real work in this condition. shutdown() only
	// emits the FIN once the send buffer and the pending writes have drained,
	// so its presence is the proof that there is nothing left to send; without
	// it, "nothing unacknowledged" is true at every momentary gap between a
	// window emptying and the next packet going out. History: HISTORY.md,
	// "updateClosingState: the half-close".
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
	c.logger.Warn("RESET from remote")

	if c.state.stateType == connClosed {
		return
	}
	// A reset answering our SYN is a refusal: nothing on the other end will
	// take the connection. libutp means to say so --
	//
	//	if (conn->close_requested) conn->state = CS_DESTROY;
	//	else conn->state = CS_RESET;
	//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET;
	//	                                        (utp_internal.cpp:2865-2870)
	//
	// -- but it overwrites the state before testing it, so the refusal is
	// unreachable and every reset reads as one. go-utp's pure Go port
	// corrects the same line. Its ICMP path, which tests the state first
	// (onICMPUnreachable), already distinguished the two here.
	err := ErrReset
	if c.state.stateType == connConnecting && c.endpoint.Type == initiator {
		err = ErrConnRefused
	}
	c.reset(err)
}

// onCloseRead applies libutp's read_shutdown: from here on, what arrives is
// acknowledged and dropped rather than delivered. See UtpStream.CloseRead for
// why the acknowledgement half matters.
func (c *connection) onCloseRead() {
	if c.readShutdown {
		return
	}
	c.readShutdown = true
	c.logger.Debug("read side closed; incoming data will be acknowledged and discarded",
		"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)

	// Whatever is already buffered is owed to a reader that has said it will
	// not come back, and holding it would keep the advertised window closed
	// by exactly that much.
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

	// If we already sent our fin and got a reset we assume the receiver already got our fin
	// and has successfully closed their connection, hence mark this as a successful close.
	if c.state.stateType == connConnected {
		if c.state.closing != nil && c.state.closing.LocalFin != nil {
			c.state.stateType = connClosed
			return
		}
	}

	// A Connect still waiting for the handshake learns why it failed. This
	// did not: a SYN answered with a reset closed the connection and left
	// the dial to wait out its own context, where libutp reports the error
	// at once (utp_call_on_error, utp_internal.cpp:2870). The timeout and
	// ICMP paths already signalled it; see onICMPUnreachable. Only during the
	// handshake: afterwards nobody waits on the channel, and its one slot may
	// still hold the success already sent.
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
