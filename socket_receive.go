package utp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
)

func (s *UtpSocket) readLoop() {
	buf := make([]byte, math.MaxUint16)

	// The inbound path starts here, so a virtual clock must not move while a
	// packet is between the wire and a connection. See idleBarrier.
	barrier, _ := s.clock().(idleBarrier)
	if barrier != nil {
		barrier.Register()
		defer func() {
			if u, ok := barrier.(interface{ Unregister() }); ok {
				u.Unregister()
			}
		}()
	}

	// Each read is a batch. A connection holds the acknowledgement owed for
	// a packet until the batch has been dispatched, so datagrams already
	// queued in the kernel draw one acknowledgement rather than one each. A
	// Conn that cannot read in batches but is a QueuedReader ends its batch
	// when nothing more is waiting; any other Conn makes each datagram its
	// own batch.
	br, _ := s.socket.(batchReader)
	qr, _ := s.socket.(QueuedReader)
	touched := &readBatch{}
	// run counts the datagrams read since the last batch ended, while a
	// QueuedReader says more are waiting.
	var failures, run int
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			// Partway through a run the read cannot block, so this loop is not
			// idle, and must not appear so while acknowledgements are owed.
			// See idleBarrier.
			if barrier != nil && run == 0 {
				barrier.MarkIdle()
			}
			if br != nil {
				dgs, err := br.readBatch()
				if barrier != nil {
					barrier.MarkBusy()
				}
				if errors.Is(err, errBatchReadUnsupported) {
					br = nil
					continue
				}
				for _, d := range dgs {
					s.dispatch(&incomingPacketRaw{peer: d.peer, payload: d.payload, borrowed: d.borrowed}, touched)
				}
				s.endBatch(touched)
				if s.readFailed(err, &failures) {
					return
				}
				continue
			}
			n, from, err := s.socket.ReadFrom(buf)
			if barrier != nil && run == 0 {
				barrier.MarkBusy()
			}
			if s.logger.Enabled(baseContext, slog.LevelDebug) {
				s.logger.Debug("read data from base socket", "n", n, "from", from)
			}
			if err != nil {
				s.endBatch(touched)
				run = 0
				if s.readFailed(err, &failures) {
					return
				}
				continue
			}
			failures = 0
			// Decoded from buf, which the next read reuses: a packet processed
			// before then needs no copy, and one that is kept copies its body
			// (packet.own).
			s.dispatch(&incomingPacketRaw{peer: from, payload: buf[:n], borrowed: true}, touched)
			if run++; qr == nil || run >= maxQueuedRun || qr.Queued() == 0 {
				s.endBatch(touched)
				run = 0
			}
		}

	}
}

// dispatch routes one datagram on the reader's goroutine. A packet for an
// established connection is handled immediately under that connection's lock
// (connection.receiveInline), and the connection is recorded in touched, to
// be acknowledged at the end of the batch. Handling packets on the reader
// avoids goroutine hand-offs between receiving a packet and acknowledging it.
func (s *UtpSocket) dispatch(raw *incomingPacketRaw, touched *readBatch) {
	s.dispatchMu.Lock()
	c := s.handleIncomingBuf(raw)
	s.dispatchMu.Unlock()
	if c != nil && !c.inReadBatch {
		c.inReadBatch = true
		touched.conns = append(touched.conns, c)
	}
}

// readBatch lists the connections one read batch delivered to, each once, in
// the order first reached. A list rather than a set, since a batch usually
// reaches one connection.
type readBatch struct {
	conns []*connection
}

// endBatch sends, for each connection a read batch delivered to, the
// acknowledgement for what the batch brought it, and empties touched.
func (s *UtpSocket) endBatch(touched *readBatch) {
	for i, c := range touched.conns {
		c.inReadBatch = false
		c.endBatch()
		touched.conns[i] = nil
	}
	touched.conns = touched.conns[:0]
}

// maxConsecutiveReadErrors is how many consecutive read errors the socket
// tolerates before it closes. Some platforms report errors on a UDP socket
// that do not mean it is finished.
const maxConsecutiveReadErrors = 100

// readFailed handles a read error and reports whether the read loop should
// stop. failures counts the errors since the last successful read. A run of
// errors is tolerated; a socket that cannot read closes, so everything
// waiting on it fails promptly.
func (s *UtpSocket) readFailed(err error, failures *int) bool {
	if err == nil {
		*failures = 0
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
		return true
	}
	*failures++
	if isTemporaryError(err) {
		s.logger.Debug("temporary UDP read error", "err", err, "consecutive", *failures)
	} else {
		s.logger.Warn("UDP read error", "err", err, "consecutive", *failures)
	}
	if *failures < maxConsecutiveReadErrors {
		return false
	}
	s.logger.Error("too many consecutive read errors, closing the socket",
		"err", err, "consecutive", *failures)
	// In the background: Close waits for this loop, among other things.
	go s.Close()
	return true
}

// handleIncomingBuf routes one datagram, and returns the connection that
// took it inline, or nil. Called with dispatchMu held.
func (s *UtpSocket) handleIncomingBuf(incomingRaw *incomingPacketRaw) *connection {
	// Handle the packet.
	packetPtr, err := decodePacket(incomingRaw.payload, incomingRaw.borrowed)
	if err != nil {
		// Not uTP, or malformed. Ordinary on a port shared with another
		// protocol, and anyone can send them, so do not log each one.
		s.logger.Debug("Unable to decode uTP packet", "peer", incomingRaw.peer, "err", err)
		return nil
	}

	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("receive incoming packet",
			"src.peer", incomingRaw.peer,
			"packet.type", packetPtr.Header.PacketType.String(),
			"packet.cid", packetPtr.Header.ConnectionId,
			"packet.seqNum", packetPtr.Header.SeqNum,
			"packet.ackNum", packetPtr.Header.AckNum,
			"packet.Data.len", len(packetPtr.Body))
	}

	// Each candidate is looked up by its key alone, built into a reused
	// buffer. A ConnectionId is built below only for a SYN.
	peerKey := incomingRaw.peer.Hash()
	send, recv, connStream, c := s.route(packetPtr, peerKey)
	if connStream != nil {
		if c != nil && c.receiveInline(packetPtr) {
			return c
		}
		// The last hop of the inbound path: a virtual clock must not consider
		// the system quiet while this packet is queued for a connection that
		// has not woken to take it. Only streamIncoming is noted and taken,
		// since the connection's other event writers (ICMP and the
		// application's close calls) are not clock participants. See
		// idleBarrier.
		barrier, _ := s.clock().(idleBarrier)
		if barrier != nil {
			barrier.NoteHandoff()
		}
		// Queued, so kept past this read. See packet.own.
		packetPtr.own()
		select {
		case connStream <- &streamEvent{Type: streamIncoming, Packet: packetPtr}:
			if c != nil {
				c.wake()
			}
		default:
			if barrier != nil {
				barrier.TakeHandoff()
			}
			// See droppedFullConnQueue. This is real packet loss the peer must
			// recover from.
			s.droppedFullConnQueue.Add(1)
			s.logger.Warn("connection stream channel is full, dropping packet",
				"connStream.len", len(connStream), "cid.send", send, "cid.recv", recv, "cid.peer", peerKey)
			return nil
		}
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("recieve a packet for a exist conn stream", "connStream.len", len(connStream))
		}
		return nil
	}

	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("received uTP packet for non-existing conn",
			"cid", packetPtr.Header.ConnectionId,
			"packetType", packetPtr.Header.PacketType,
			"seq", packetPtr.Header.SeqNum,
			"ack", packetPtr.Header.AckNum,
			"peerInitCID", cidFromPacket(packetPtr, incomingRaw.peer, cidTypes[0]),
			"weInitCid", cidFromPacket(packetPtr, incomingRaw.peer, cidTypes[1]),
			"accCID", cidFromPacket(packetPtr, incomingRaw.peer, cidTypes[2]))
	}
	if packetPtr.Header.PacketType != st_syn {
		// An unmatched RESET is dropped, never answered: answering a reset
		// with a reset would let two hosts exchange them indefinitely.
		if packetPtr.Header.PacketType != st_reset {
			// A FIN or data packet retransmitted after we closed is
			// re-acknowledged rather than reset. Checked only after every
			// lookup above has missed, so a live connection always wins.
			if s.reAckLingering(packetPtr, incomingRaw.peer) {
				return nil
			}
			s.maybeSendReset(packetPtr, incomingRaw.peer)
		}
		return nil
	}

	// A SYN is kept, by the connection it starts or until an Accept takes
	// it. See packet.own.
	packetPtr.own()
	cid := cidFromPacket(packetPtr, incomingRaw.peer, cidTypes[2])
	cidHash := cid.Hash()

	// A connection we dialed may already receive on the id this one would
	// use; accepting would leave a packet ambiguous between the two. The SYN
	// is dropped without an answer, and the dialer retries or gives up.
	s.connsMutex.RLock()
	taken := s.recvIdTakenLocked(cid.Recv, peerKey)
	s.connsMutex.RUnlock()
	if taken {
		s.refusedIdInUse.Add(1)
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("refusing an incoming connection: its receive id is in use",
				"src.peer", incomingRaw.peer, "recv", cid.Recv)
		}
		return nil
	}

	// Check the connection limit after the duplicate lookup and before the
	// firewall. A SYN for a connection already waiting for Accept is a
	// retransmission and does not count.
	if s.maxConns > 0 {
		if _, parked := s.incomingConns.get(cidHash); !parked && s.connectionCount() > s.maxConns {
			s.refusedAtCapacity.Add(1)
			if s.logger.Enabled(baseContext, slog.LevelDebug) {
				s.logger.Debug("refusing an incoming connection: at capacity",
					"src.peer", incomingRaw.peer, "cap", s.maxConns)
			}
			return nil
		}
	}

	// Consult the firewall: every lookup above has missed, so this is a new
	// connection and nothing has been created for it yet. A refused SYN is
	// dropped without an answer; a RESET would confirm to a blocked peer
	// that something is listening.
	if s.firewall != nil && s.firewall(incomingRaw.peer) {
		s.refusedByFirewall.Add(1)
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("refusing an incoming connection: firewall", "src.peer", incomingRaw.peer)
		}
		return nil
	}

	s.logger.Debug("receive a syn packet from a new conn stream",
		"src.peer", incomingRaw.peer,
		"cid.Send", cid.Send,
		"cid.Recv", cid.Recv,
		"cid.hash", cidHash)

	if accept, exist := s.getAwaiting(cidHash); exist {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("found a accept request from awaiting map...",
				"accept.cid.Send", accept.cid.Send, "accept.cid.Recv", accept.cid.Recv)
		}
		s.removeAwaiting(cidHash)
		connected := make(chan error, 1)
		newConnStream := make(chan *streamEvent, connEventQueueLen)
		s.putConnStream(cid, newConnStream)
		stream := newUtpStream(s.ctx, s.logger, cid, s.configForAccept(accept, cid.Peer), packetPtr, nil, s.sendEvent, newConnStream, connected, s.retransmitTimers)
		s.attachInline(cidHash, newConnStream, stream.conn)
		stream.linger = s.linger
		go s.awaitConnected(stream, accept, connected)
	} else if accept := s.takePendingAccept(); accept != nil {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("handing a new SYN to a waiting Accept",
				"cid.Send", cid.Send, "cid.Recv", cid.Recv)
		}
		// The socket's context, not the Accept call's: an accepted connection
		// outlives the Accept that returned it, as a dialed one outlives its
		// dial.
		s.selectAcceptHelper(cid, packetPtr, accept)
	} else if s.acceptConfig != nil {
		// Nobody is waiting and the socket has its own configuration: answer
		// now and hold the connection for an Accept. It is held from here,
		// under the lock Accept takes, so the backlog reflects it before the
		// dialer can see the answer.
		if st := s.selectAcceptHelper(cid, packetPtr, &acceptRequest{
			ctx:    s.ctx,
			stream: make(chan *streamResult, 1),
			// Already held. A connection that fails is dropped from answered once
			// it ends.
			onHand: func(*streamResult) {},
		}); st != nil {
			s.holdAnswered(st)
		}
	} else {
		s.logger.Debug("put a new syn packet to incomingConns...")
		s.putIncomingConn(cidHash, &incomingPacket{pkt: packetPtr, cid: cid})
	}
	return nil
}

// rememberLingerAck keeps one acknowledgement for a connection that has just
// closed, so a FIN the peer retransmits is answered rather than reset.
// lingerAckTimeout covers the first few retransmissions, where a lost
// acknowledgement matters; after that the peer gives up and a RESET is the
// right answer.
func (s *UtpSocket) rememberLingerAck(key string, ack *packet) {
	if ack == nil {
		return
	}
	s.lingerAcks.put(key, ack)
	s.lingerExpirations.put(key, key, lingerAckTimeout)
}

// reAckLingering answers a FIN or data packet retransmitted for a connection
// that has just closed, and reports whether it did.
//
// Both arrive because one of our final acknowledgements was lost and the
// peer is retransmitting what we already received. Acknowledging it again
// ends the connection cleanly on both sides; a RESET would tell the peer a
// completed transfer failed.
//
// Data beyond the stored acknowledgement is the peer writing after our FIN.
// With no application left to deliver it to, it is dropped silently. Either
// way the answer is never a RESET.
func (s *UtpSocket) reAckLingering(pkt *packet, peer ConnectionPeer) bool {
	if pkt.Header.PacketType != st_fin && pkt.Header.PacketType != st_data {
		return false
	}
	for _, cidType := range cidTypes {
		cid := cidFromPacket(pkt, peer, cidType)
		ack, ok := s.lingerAcks.get(cid.Hash())
		if !ok {
			continue
		}
		// Data past the stored acknowledgement number is the peer writing after
		// our FIN, which a half-close permits. Nobody is left to receive it,
		// so it is discarded silently rather than answered with a RESET.
		//
		// The peer's FIN, numbered just past everything we took, ends a
		// stream we have in full: acknowledge it and move the stored
		// acknowledgement to cover it, or a peer that closed second would
		// retransmit its FIN indefinitely. It is stamped as sent
		// (freshlyStamped) on the wire clock, since the connection is gone.
		if pkt.Header.PacketType == st_fin && pkt.Header.SeqNum == ack.Header.AckNum+1 {
			finAck := freshlyStamped(ack, nowMicro(), pkt)
			finAck.Header.AckNum = pkt.Header.SeqNum
			s.lingerAcks.put(cid.Hash(), finAck)
			s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
			s.sendEvent(newOutgoingSocketEvent(finAck, cid.Peer))
			return true
		}
		if wrappingLessThan(ack.Header.AckNum, pkt.Header.SeqNum) {
			if s.logger.Enabled(baseContext, slog.LevelDebug) {
				s.logger.Debug("ignoring data past a closed connection's FIN",
					"cid.send", cid.Send, "cid.recv", cid.Recv, "seq", pkt.Header.SeqNum)
			}
			// Refresh, as below: a peer still writing is still there, and letting
			// the entry expire would hand it the RESET this avoids.
			s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
			return true
		}
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("re-acking a FIN for a connection that has closed",
				"cid.send", cid.Send, "cid.recv", cid.Recv)
		}
		// Refresh the entry: a peer still retransmitting is still waiting.
		s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
		s.sendEvent(newOutgoingSocketEvent(freshlyStamped(ack, nowMicro(), pkt), cid.Peer))
		return true
	}
	return false
}

// maybeSendReset answers a packet for a connection this socket does not have
// with a RESET. Each packet (peer, connection id and sequence number) is
// answered once while its entry lasts, and nothing is answered while too many
// entries are outstanding, so a RESET cannot be used for amplification.
func (s *UtpSocket) maybeSendReset(pkt *packet, peer ConnectionPeer) {
	key := rstInfoKey{
		peer:   peer.Hash(),
		connID: pkt.Header.ConnectionId,
		ackNr:  pkt.Header.SeqNum,
	}

	if _, seen := s.rstInfo.get(key); seen {
		// Already answered: refresh its expiry and stay quiet.
		s.rstInfoExpirations.put(key, key, rstInfoTimeout)
		return
	}
	if s.rstInfo.len() > rstInfoLimit {
		// Stop answering rather than let the table grow without bound.
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("not sending RESET, too many already outstanding", "stored", s.rstInfo.len())
		}
		return
	}

	s.resetsSent.Add(1)
	s.rstInfo.put(key, struct{}{})
	s.rstInfoExpirations.put(key, key, rstInfoTimeout)

	// The reset's fields:
	//
	//   - ack_nr is the sequence number of the packet being rejected, so the
	//     peer can tell which packet drew the reset;
	//   - seq_nr is random, so an off-path attacker cannot predict it;
	//   - windowsize is zero, since there is no connection;
	//   - the timestamp fields are zero.
	randSeqNum := randomUint16()
	resetPacket := newPacketBuilder(st_reset, pkt.Header.ConnectionId, 0, 0, randSeqNum).
		WithAckNum(pkt.Header.SeqNum).
		Build()
	s.sendEvent(newOutgoingSocketEvent(resetPacket, peer))
}

// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can use the router's figure instead of losing
// a probe to discover it.
//
// quoted is the uTP datagram the ICMP message quoted, the original UDP
// payload, without the ICMP, IP or UDP headers. peer is the address it was
// sent to. nextHopMTU is the router's figure, which may be zero; the search
// halves the gap when it is missing or implausible.
//
// It reports whether quoted was recognized as a packet of a live connection.
//
// The socket does not collect ICMP itself. On Linux the usual source is
// IP_RECVERR on the UDP socket.
func (s *UtpSocket) ProcessICMPFragmentation(quoted []byte, peer ConnectionPeer, nextHopMTU uint16) bool {
	return s.deliverICMP(quoted, peer, &icmpNotice{
		Kind:       icmpFragmentationNeeded,
		NextHopMTU: uint32(nextHopMTU),
	})
}

// ProcessICMPError reports an ICMP message that should end a connection:
// destination unreachable other than fragmentation needed, time exceeded, or
// parameter problem. The connection ends with ErrConnRefused if it had only
// sent its SYN and ErrReset otherwise.
//
// quoted, peer and the result are as for ProcessICMPFragmentation. Which ICMP
// messages are fatal is for the caller to decide.
func (s *UtpSocket) ProcessICMPError(quoted []byte, peer ConnectionPeer) bool {
	return s.deliverICMP(quoted, peer, &icmpNotice{Kind: icmpUnreachable})
}

// deliverICMP parses the quoted header and delivers the report to its
// connection.
func (s *UtpSocket) deliverICMP(quoted []byte, peer ConnectionPeer, notice *icmpNotice) bool {
	if peer == nil || notice == nil {
		return false
	}

	// ICMP need only quote 8 bytes of the layer-4 payload, but the UDP
	// header is already stripped, so the 20-byte uTP header is required.
	if len(quoted) < minimalHeaderSize {
		s.logger.Debug("ignoring ICMP: runt quoted payload", "len", len(quoted), "peer", peer)
		return false
	}
	// Parse only the fixed header: a router that quoted 20 bytes has cut
	// off the extensions, and requiring them would discard the report.
	header, err := decodePacketHeader(quoted[:minimalHeaderSize])
	if err != nil {
		s.logger.Debug("ignoring ICMP: quoted payload is not a uTP v1 packet",
			"peer", peer, "err", err)
		return false
	}

	connStream := s.getConnStreamForQuotedId(header.ConnectionId, peer)
	if connStream == nil {
		s.logger.Debug("ignoring ICMP: no matching connection",
			"id", header.ConnectionId, "peer", peer)
		return false
	}

	select {
	case connStream <- &streamEvent{Type: streamICMP, ICMP: notice}:
		s.connsMutex.RLock()
		c := s.inlineConns[connStream]
		s.connsMutex.RUnlock()
		if c != nil {
			c.wake()
		}
	default:
		// The connection's queue is full. Dropping the report costs a probe,
		// not correctness: fragmentation is rediscovered by the lost probe
		// and the error by the retransmission timeout. The connection was
		// found, so this still reports recognized.
		s.logger.Warn("connection stream channel is full, dropping ICMP report",
			"id", header.ConnectionId, "peer", peer)
	}
	return true
}

func (s *UtpSocket) getConnStreamForQuotedId(id uint16, peer ConnectionPeer) chan *streamEvent {
	candidates := []*ConnectionId{
		// recv == id (the quoted packet is our SYN), either role
		NewConnectionId(peer, id, id+1),
		NewConnectionId(peer, id, id-1),
		// recv == id+1 && send == id: we accepted this connection
		NewConnectionId(peer, id+1, id),
		// recv == id-1 && send == id: we initiated it
		NewConnectionId(peer, id-1, id),
	}
	for _, cid := range candidates {
		if ch, ok := s.getConnStream(cid.Hash()); ok {
			return ch
		}
	}
	return nil
}
