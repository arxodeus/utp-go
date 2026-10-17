package utp

import (
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

// cancel withdraws what the connection has not yet taken, and reports what
// it has: the count of bytes already in the send buffer, which will be sent,
// or the result, if the whole write was taken before the writer gave up.
//
// A write used to return 0 on cancellation while the connection went on
// taking the rest of it, so its bytes were sent after the caller had been
// told none were -- and utpnet, which retries a write whose deadline moved
// and that reported nothing written, sent them twice. From the release
// review of 9c44c4f (R1).
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
// spoke.
//
// Every outbound packet goes through here so that the keep-alive can tell
// whether the connection has been silent, which is libutp's
// `last_sent_packet` (utp_internal.cpp:1272).
func (c *connection) emit(pkt *packet) {
	c.emitPacket(pkt, false)
}

// emitPacket sends a packet, through the socket. dontFragment marks
// it as an MTU probe; see DontFragmentWriter.
func (c *connection) emitPacket(pkt *packet, dontFragment bool) {
	if pkt == nil {
		return
	}
	c.lastSentPacket = c.now()
	if pkt.Header != nil {
		c.lastAdvertisedWindow = pkt.Header.WndSize
	}
	if pkt.Header.PacketType == st_state {
		// Kept for the socket to repeat after this connection is gone.
		c.lastStateSent = pkt
	}
	if c.out != nil {
		// Straight to the wire, on this goroutine: nothing is in flight
		// between deciding to send and sending, so a virtual clock has
		// nothing to wait for. The event is done with when out returns, so
		// one serves every packet.
		c.outEvent = socketEvent{Type: outgoing, Packet: pkt, ConnectionId: c.cid, DontFragment: dontFragment}
		c.out(&c.outEvent)
		c.outEvent = socketEvent{}
		return
	}
	ev := newOutgoingSocketEvent(pkt, c.cid)
	if dontFragment {
		ev = newOutgoingProbeSocketEvent(pkt, c.cid)
	}
	// A virtual clock must not consider the system quiet while this packet is
	// queued but not yet taken by whoever reads socketEvents. See
	// idleBarrier.NoteHandoff.
	if b, ok := c.timeSource().(idleBarrier); ok {
		b.NoteHandoff()
	}
	c.socketEvents <- ev
}

// keepAlivePacket is a STATE acknowledging one less than we actually have.
//
// libutp: `ack_nr--; send_ack(); ack_nr++` (utp_internal.cpp:834-844). Acking
// one behind makes the packet look like a stale acknowledgement to the peer,
// which answers it -- so the exchange proves both directions still work
// without consuming a sequence number or delivering anything.
func (c *connection) keepAlivePacket() *packet {
	pkt := c.statePacket()
	if pkt == nil {
		return nil
	}
	pkt.Header.AckNum-- // wrapping
	// The selective ack must be read against the decremented number too.
	// libutp decrements ack_nr before send_ack builds the mask
	// (utp_internal.cpp:836-843, :804-808), so its bit i is ack_nr + 1 + i
	// of the real acknowledgement; ours was built against the real one, bit i
	// being ack_nr + 2 + i. Shift it up one: the new bit 0 is ack_nr + 1, the
	// packet a selective ack exists because we lack, and the top bit falls
	// out of the window as it does in libutp. Left unshifted, the peer read
	// every bit one packet early and could free a packet we never received.
	// TestKeepAliveSelectiveAckMatchesItsAckNumber.
	if pkt.Eack != nil {
		old := pkt.Eack.Acked()[:selectiveAckWindow]
		shifted := make([]bool, len(old))
		copy(shifted[1:], old[:len(old)-1])
		pkt.Eack = newSelectiveAck(shifted)
	}
	return pkt
}

// keepAliveIntervalOrDefault is the configured interval, or libutp's.
func (c *connection) keepAliveIntervalOrDefault() time.Duration {
	if c.config.KeepAliveInterval > 0 {
		return c.config.KeepAliveInterval
	}
	return defaultKeepAliveInterval
}

// zeroWindowProbeIntervalOrDefault is the configured interval, or libutp's.
func (c *connection) zeroWindowProbeIntervalOrDefault() time.Duration {
	if c.config.ZeroWindowProbeInterval > 0 {
		return c.config.ZeroWindowProbeInterval
	}
	return defaultZeroWindowProbeInterval
}

// effectivePeerWindow is how much the peer says it can accept, with the
// zero-window probe applied.
//
// libutp does this by overwriting max_window_user with PACKET_SIZE once
// zerowindow_time has passed (utp_internal.cpp:1142-1145); the next
// acknowledgement overwrites it again with whatever the peer reports. The
// effect is the same: exactly one packet gets through a closed window per
// interval, and the peer's answer to it carries a fresh window.
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
		// LEDBAT++'s slowdowns are driven by the clock, not by acks. Without
		// this, a connection whose application goes quiet mid-slowdown would
		// stay pinned at two packets until an ack happened to arrive.
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
		// told why -- with ErrNotConnected for a connection that closed
		// cleanly, which has no error of its own -- and how many were taken
		// before the end. Both used to be wrong: a nil error with nothing
		// written, which an io.Writer may not return, and 0 for a writer
		// part of whose bytes were already in the send buffer.
		err := c.state.Err
		if err == nil {
			err = ErrNotConnected
		}
		// Each writer is answered once. The queue used to be left as it was,
		// so the next call sent to every writer again, and a result channel
		// holds one: the second send blocked for ever, with the connection's
		// lock held -- and the socket's reader, which serves every connection,
		// waiting behind it.
		for _, w := range c.pendingWrites {
			w.resultCh <- &readOrWriteResult{Len: w.written, Err: err}
		}
		c.pendingWrites = nil
		return
	default:
	}

	// Compose data packets.
	//
	// libutp queues a write as packets of up to packet_size and flush_packets
	// sends the next one while !is_full() (utp_internal.cpp:3197-3212,
	// :963-985). is_full is called there with no argument, so it charges a
	// whole packet_size whatever the queued packet actually holds (:933-934),
	// and refuses when cur_window + packet_size > min(max_window, opt_sndbuf,
	// max_window_user) (:936, :956). Two things follow, and both used to be
	// different here:
	//
	//   - Bytes in flight count against the peer's window, not only against
	//     the congestion window. The peer's advertisement is how much it can
	//     take beyond what it has already acknowledged; everything we have
	//     sent since is already spending it. Without this we overran a
	//     receiver's buffer by up to a full window whenever its application
	//     fell behind, and every packet past its room was dropped and had to
	//     be retransmitted.
	//   - Nothing is sent into less than a full packet of room, and a packet
	//     is never cut down to fit what room there is. We used to fill a
	//     window to the byte with whatever size fitted.
	//
	// opt_sndbuf has no counterpart: our send buffer bounds what the
	// application may queue, not what may be in flight.
	packetSize := c.mtu.payloadSize()
	maxSend := minUint32(c.state.SentPackets.CongestionWindow(), c.effectivePeerWindow(now))
	// And never more than maxOutstandingPackets in flight, however small
	// they are. libutp's is_full refuses at `cur_window_packets >=
	// OUTGOING_BUFFER_MAX_SIZE - 1` before it looks at bytes
	// (utp_internal.cpp:939-947), so the flush stops there, resends
	// included. There was no such bound here: a run of small writes into a
	// large window could put more packets in flight than a libutp receiver
	// will reorder (1024, :1890) -- and, past 32768, more than 16-bit
	// sequence numbers can order at all.
	outstanding := int(c.state.SentPackets.UnackedCount())

	// Packets a retransmission timeout gave up as lost go first, oldest
	// first, under the same rule as new data. libutp's flush_packets walks
	// its outgoing buffer from the oldest packet and sends each one that is
	// unsent or need_resend while !is_full() (utp_internal.cpp:970-985), so
	// a resend always precedes the new data behind it.
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
	// Taken, not shared: nothing here should come back into processWrites,
	// but if it did it would start a slice of its own.
	payloads := c.payloadScratch[:0]
	c.payloadScratch = nil
	var composed uint32

	// libutp's `is_full` marks the connection application-limited or not
	// every time it considers sending a packet (utp_internal.cpp:945, :957),
	// and the congestion controller refuses to grow a window the application
	// never fills (:1681-1686). The equivalent signal here is "we had data
	// and no room for it".
	windowFull := resendsWaiting

	for !resendsWaiting && c.state.SendBuf.Pending() > 0 {
		if outstanding+len(payloads) >= maxOutstandingPackets ||
			uint64(inFlight)+uint64(composed)+uint64(packetSize) > uint64(maxSend) {
			windowFull = true
			break
		}
		n := minUint32(uint32(c.state.SendBuf.Pending()), packetSize)
		// libutp's Nagle rule (flush_packets, utp_internal.cpp:974-982): the
		// last packet waits while it is short of a full one and anything is
		// ahead of it in the window -- sent and unacknowledged, or composed in
		// this pass -- and what is written next joins it. The acknowledgement
		// that empties the window releases it, as libutp's "flush Nagle" does
		// (:2246-2252): every acknowledgement brings this loop round again.
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

	// Write pending data to send buffer
	for len(c.pendingWrites) > 0 {
		writeReq := c.pendingWrites[0]
		writeReq.mu.Lock()
		// A write its caller gave up on is dropped whether or not there is
		// room, so that it does not hold a FIN behind it.
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

		// The queued data is the stream's own copy of what was written
		// (writeQueued), so the send buffer takes it as it is.
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

	// transmit data packets
	seqNum := c.state.SentPackets.NextSeqNum()
	recvWindow := c.recvWindow()
	ackNum := c.state.RecvBuf.AckNum()

	for _, payload := range payloads {
		// No selective ack: see statePacket.
		packetInst := newPacketBuilder(
			st_data,
			c.cid.Send,
			c.nowMicros(),
			recvWindow,
			seqNum,
		).WithPayload(payload).WithTsDiffMicros(uint32(c.peerTsDiff.Microseconds())).WithAckNum(ackNum).Build()

		c.transmit(packetInst, now, true)
		seqNum = seqNum + 1 // wrapping add in uint16
	}
	clear(payloads)
	c.payloadScratch = payloads[:0]
}

func (c *connection) onWrite(writeReq *queuedWrite) {
	switch c.state.stateType {
	case connConnecting:
		// There are 0 bytes written so far
		c.pendingWrites = append(c.pendingWrites, writeReq)

	case connConnected:
		if c.state.closing != nil {
			if c.logger.Enabled(baseContext, LevelTrace) {
				c.logger.Trace("append a queuedWrite to pending writes when closing the conn...")
			}
			if c.state.closing.LocalFin == nil && c.state.closing.RemoteFin != nil {
				c.pendingWrites = append(c.pendingWrites, writeReq)
			} else {
				// Our FIN is out: nothing more may be sent. libutp's
				// utp_writev refuses the same (`if (conn->fin_sent)
				// return 0`, utp_internal.cpp:3188). This answered Len 0
				// with no error, which an io.Writer may not do: a write
				// queued just before Close or CloseWrite reported success
				// and its bytes were never sent.
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

// maxOutstandingPackets is how many packets may be in flight at once:
// libutp's `OUTGOING_BUFFER_MAX_SIZE - 1` (utp_internal.cpp:52, applied in
// is_full at :939). See processWrites.
const maxOutstandingPackets = 1024 - 1

// defaultZeroWindowProbeInterval is how long libutp tolerates a closed peer
// window before forcing a packet through: "Reset max_window_user to 1 every 15
// seconds" (utp_internal.cpp:2150-2151).
const defaultZeroWindowProbeInterval = 15 * time.Second

// defaultKeepAliveInterval is how long libutp lets an established connection
// stay silent before sending a keep-alive: `#define KEEPALIVE_INTERVAL 29000`
// (utp_internal.cpp:74), applied at :1271-1274.
//
// 29 seconds is chosen to sit under the 30-second UDP mapping timeout common
// in NATs. A connection that goes quiet for longer than that loses its
// mapping and cannot be reached again from the outside.
const defaultKeepAliveInterval = 29 * time.Second

func (c *connection) synPacket(seqNum uint16) *packet {
	nowMicros := int64(c.nowMicros())
	return newPacketBuilder(
		st_syn,
		c.cid.Recv,
		uint32(nowMicros),
		c.config.WindowSize,
		seqNum,
	).Build()
}

// transmit sends a packet and arms its retransmission timer.
//
// firstTransmission distinguishes a packet going out for the first time from
// one being resent. Only the former can serve as an MTU probe: libutp excludes
// retransmissions because an oversized packet being resent needs to fragment
// just to get through, which is the opposite of what a probe is for
// (utp_internal.cpp:900-904).
func (c *connection) transmit(packet *packet, now time.Time, firstTransmission bool) {
	var payload []byte
	var length uint32

	if len(packet.Body) > 0 {
		payload = packet.Body
		length = uint32(len(packet.Body))
	}

	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("will transmit packet",
			"cid", packet.Header.ConnectionId,
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
		// A probe sent again goes without the don't-fragment bit, and
		// fragmented if it has to be, so its acknowledgement no longer says
		// the size fits: forget the probe and let the search send another.
		// libutp's only resends of a probe come after it has been judged
		// or cleared -- the third duplicate acknowledgement
		// (utp_internal.cpp:1927-1938), the timeout (:1152-1167) -- and so
		// did ours until the loss probe, which resends the oldest packet
		// outstanding and can pick the probe. Its fragmented copy was
		// acknowledged and the search settled on 1402 bytes for a path that
		// had just dropped a 1402-byte probe
		// (netem.TestDontFragmentBringsTheSearchWithinThePath, under load).
		if c.mtu.probeOutstanding(packet.Header.SeqNum) {
			c.mtu.clearProbe()
		}
	}

	// Use this packet as an MTU probe if the search wants one.
	//
	// libutp decides the same thing in send_packet (utp_internal.cpp:906-925)
	// and sends the probe with fragmentation disabled, by passing
	// UTP_UDP_DONTFRAG to its embedder's sendto callback (:925-929). This
	// carries the same bit the same way: the flag travels with the datagram to
	// the socket, which asks the Conn to honour it if it can.
	//
	// A Conn that cannot -- the libutp driver, an emulated network that does
	// not model fragmentation, an operating system with no such socket option
	// -- sends it normally, which is what every probe did before this existed.
	// The cost of that is real and is why the bit is worth carrying: on IPv4 a
	// router may fragment an oversized probe rather than drop it, so the probe
	// is acknowledged, the floor rises, and the search settles on a size that
	// works only because every packet at it is being fragmented.
	isProbe := false
	if datagramSize := uint32(packet.EncodedLen()); c.mtu.eligibleProbe(datagramSize, firstTransmission) {
		c.mtu.beginProbe(packet.Header.SeqNum, datagramSize)
		isProbe = true
		if c.logger.Enabled(baseContext, slog.LevelDebug) {
			c.logger.Debug("MTU probe", "size", datagramSize,
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "seq", packet.Header.SeqNum)
		}
	}

	c.state.SentPackets.OnTransmit(packet.Header.SeqNum, packet.Header.PacketType, payload, length, now)
	c.armRetransmit(packet, c.state.SentPackets.Timeout())

	// A packet carrying the current acknowledgement -- and the current
	// selective ack, which every packet this connection builds carries --
	// says everything a deferred ST_STATE would have, so that one is no
	// longer owed. libutp takes the socket off its ack list whenever it sends
	// anything (send_data, utp_internal.cpp:768).
	//
	// It rarely applies here: data received and data sent mostly fall in
	// different event-loop passes. Measured in a two-way transfer against
	// libutp, before socket reads were batched, pure acknowledgements per data
	// packet were 0.90 before and after it (DEVIATIONS.md, "Acknowledgements:
	// one per read, fewer when they would crowd the way back").
	if c.state.RecvBuf != nil && packet.Header.AckNum == c.state.RecvBuf.AckNum() {
		c.ackPending = false
		c.ackHeldSince = time.Time{}
		c.dataSinceAck = 0
	}

	c.emitPacket(packet, isProbe)
}
