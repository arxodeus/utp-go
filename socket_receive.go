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
	// a packet until the batch has been dispatched, so a run of datagrams that
	// were already waiting in the kernel draws one acknowledgement, not one
	// each -- libutp's embedder loop of recvfrom until EWOULDBLOCK, then
	// utp_issue_deferred_acks (utp.h:512-517). A Conn that cannot read in
	// batches but is a QueuedReader has its batch end when nothing more is
	// waiting; any other Conn makes every datagram its own batch, which holds
	// nothing back.
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
			// Part way through a run the read cannot block, so this loop is
			// not idle -- and must not be seen to be, with acknowledgements
			// still owed for the run. See idleBarrier.
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
			// Decoded from buf, which the next read reuses: a packet
			// processed before then needs no copy, and one kept takes its
			// body (packet.own). The whole datagram was copied, an
			// allocation for every packet received.
			s.dispatch(&incomingPacketRaw{peer: from, payload: buf[:n], borrowed: true}, touched)
			if run++; qr == nil || run >= maxQueuedRun || qr.Queued() == 0 {
				s.endBatch(touched)
				run = 0
			}
		}

	}
}

// dispatch routes one datagram, on the reader's goroutine. A packet for an
// established connection is handled there and then, under that
// connection's lock (connection.receiveInline), and the connection is
// recorded in touched, to be acknowledged at the end of the batch.
//
// This handed every datagram to a socket event loop, which handed it on to
// the connection's own event loop: two goroutine hand-offs before the packet
// was looked at, and a third, to a write loop, before its acknowledgement
// left. libutp's embedder calls utp_process_udp from its read loop and the
// acknowledgement is sent from the same call stack.
func (s *UtpSocket) dispatch(raw *incomingPacketRaw, touched *readBatch) {
	s.dispatchMu.Lock()
	c := s.handleIncomingBuf(raw)
	s.dispatchMu.Unlock()
	if c != nil && !c.inReadBatch {
		c.inReadBatch = true
		touched.conns = append(touched.conns, c)
	}
}

// readBatch is the connections one read batch delivered to, each once, in the
// order the batch first reached them. A list rather than a set: a batch
// usually reaches one connection, and a map's iteration and deletion were a
// tenth of a packet's cost on a reader woken with its caches cold.
type readBatch struct {
	conns []*connection
}

// endBatch acknowledges, for each connection a read batch delivered to, what
// the batch brought it, and empties touched. libutp's
// utp_issue_deferred_acks, which its embedder calls once the socket would
// block (utp.h:512-517).
func (s *UtpSocket) endBatch(touched *readBatch) {
	for i, c := range touched.conns {
		c.inReadBatch = false
		c.endBatch()
		touched.conns[i] = nil
	}
	touched.conns = touched.conns[:0]
}

// maxConsecutiveReadErrors is how many read errors in a row the socket
// tolerates before it gives up and closes. go-libutp, go-utp's pure Go port
// and rust-utp all use 100, for the same reason: some platforms report errors
// on a UDP socket that do not mean it is finished.
const maxConsecutiveReadErrors = 100

// readFailed handles a read error, and reports whether the read loop should
// stop. failures counts the errors since the last successful read.
//
// The loop used to stop at the first error that did not say it was temporary,
// and leave the socket open. Nothing read from it again: every connection on
// it retransmitted into silence until it timed out, new dials and accepts
// waited out their contexts, and nothing told the application why. Now a run
// of errors is tolerated, and a socket that cannot read closes, so everything
// waiting on it fails at once with ErrClosed-shaped errors instead.
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
	// In the background: Close waits for this loop's peers to settle, and
	// this loop is one of the things it tears down.
	go s.Close()
	return true
}

// handleIncomingBuf routes one datagram, and returns the connection that
// took it inline, or nil. Called with dispatchMu held.
func (s *UtpSocket) handleIncomingBuf(incomingRaw *incomingPacketRaw) *connection {
	// Handle incoming packets
	packetPtr, err := decodePacket(incomingRaw.payload, incomingRaw.borrowed)
	if err != nil {
		// Not uTP, or not well formed. Ordinary on a port shared with
		// another protocol -- go-libutp, go-utp and rust-utp all hand such
		// datagrams on rather than complain -- and anyone can send them, so
		// a warning per datagram is a way to flood the log.
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

	// Each derivation is looked up by its key alone, built into a reused
	// buffer: no ConnectionId and no string per attempt. This ran three
	// cidFromPacket calls for every datagram -- for an accepted connection the
	// match is the third -- each formatting the peer's address and the key
	// anew: 9 of the 23 allocations a data packet cost, and most of the time
	// routing took. A ConnectionId is built below only for a SYN.
	peerKey := incomingRaw.peer.Hash()
	send, recv, connStream, c := s.route(packetPtr, peerKey)
	if connStream != nil {
		if c != nil && c.receiveInline(packetPtr) {
			return c
		}
		// The last hop of the inbound path: a virtual clock must not
		// consider the system quiet while this packet is queued for a
		// connection that has not woken to take it.
		//
		// Noted and taken on streamIncoming only, and that is what keeps
		// the accounting honest rather than approximately right. A
		// connection's event channel has several writers -- this loop,
		// the ICMP entry points, and the application's own CloseWrite,
		// CloseRead and Close -- and only this one is a clock
		// participant. Keying both halves on the event type makes the
		// pairing structural: nothing else notes, and the connection
		// takes for nothing else. See idleBarrier.
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
			// See droppedFullConnQueue. This is a real packet loss that the
			// peer has to recover from, and it is ours, not the network's.
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
		// An unmatched RESET is dropped, never answered: replying to a reset
		// with a reset lets two hosts trade them forever. libutp handles
		// ST_RESET in its own branch and returns without responding
		// (utp_internal.cpp:2850-2881).
		if packetPtr.Header.PacketType != st_reset {
			// A FIN or data packet retransmitted after we closed is
			// re-acknowledged rather than reset. Checked here, once every
			// lookup above has missed, so a connection that still exists
			// always wins.
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

	// A connection we dialled may already receive on the id this one would:
	// accepting it would leave two connections a packet could be for. libutp
	// refuses the SYN without an answer -- "rejected incoming connection,
	// connection already exists" (utp_internal.cpp:2957-2965) -- and the
	// dialler, hearing nothing, gives up or retries on its own terms.
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

	// The connection cap is checked here, where libutp checks it: after the
	// duplicate lookup above and before the firewall
	// (utp_internal.cpp:2967-2974). A SYN for a connection already parked
	// waiting for Accept is the peer retransmitting, not a new connection,
	// and does not count against the cap.
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

	// The firewall is asked here: every lookup above has missed, so this is a
	// connection the socket does not have, and nothing has been created for it
	// yet. libutp asks in the same place, after its duplicate-connection check
	// and before utp_create_socket (utp_internal.cpp:2975-2982), and a refusal
	// there is `return 1` -- the SYN is dropped without an answer. Not a
	// RESET: a RESET would confirm to a refused peer that something is
	// listening, which is the opposite of what a blocklist is for.
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
		// outlives the Accept that produced it, exactly as a dialled one
		// outlives its dial. The sibling branch above already used s.ctx.
		s.selectAcceptHelper(cid, packetPtr, accept)
	} else if s.acceptConfig != nil {
		// Nobody is waiting, and the socket has a configuration of its own:
		// answer now, as libutp does, and hold the connection for an Accept.
		//
		// Held from here, under the lock an Accept takes it under, not once
		// its handshake reports from another goroutine: by then the dialler
		// can have the answer, and an AcceptBacklog asked of this socket
		// found nothing -- TestDialCompletesWithNoAcceptWaiting, 9 runs in
		// 200. libutp hands its embedder the connection from inside the call
		// that received the SYN (utp_call_on_accept, utp_internal.cpp:3001).
		if st := s.selectAcceptHelper(cid, packetPtr, &acceptRequest{
			ctx:    s.ctx,
			stream: make(chan *streamResult, 1),
			// Already held. A connection that fails ends, and is dropped
			// from answered once it has.
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

// maybeSendReset answers a packet addressed to a connection this socket does
// not have, subject to the same rate limiting libutp applies.
//
// Answering every such packet is both an incompatibility and an amplification
// vector: a peer that keeps sending to a torn-down connection draws one RESET
// per packet. A 300-connection test run emitted over 3000 of them. libutp
// remembers what it has already answered and stays quiet
// (utp_internal.cpp:2907-2945).
// rememberLingerAck keeps one acknowledgement for a connection that has just
// closed, so a FIN the peer retransmits is answered rather than reset.
//
// lingerAckTimeout is generous against libutp's FIN retransmissions, which
// back off from its 1000ms RTO floor: it covers the first few, which is where
// a lost acknowledgement actually costs something. Past that the peer gives up
// on its own and a RESET is the right answer anyway.
func (s *UtpSocket) rememberLingerAck(key string, ack *packet) {
	if ack == nil {
		return
	}
	s.lingerAcks.put(key, ack)
	s.lingerExpirations.put(key, key, lingerAckTimeout)
}

// reAckLingering answers a FIN or a data packet retransmitted for a connection
// that has just closed, and reports whether it did.
//
// Both arrive for the same reason: the peer finished sending, we acknowledged
// everything including its FIN and tore down, and one of those acknowledgements
// was lost. The peer is retransmitting something we have already received and
// already acknowledged. Sending it that acknowledgement again ends the
// connection cleanly on both sides; sending a RESET tells it a completed
// transfer failed.
//
// This is what libutp does in return, for the same reason it can: it keeps the
// socket in CS_GOT_FIN until its own application closes, and a socket in that
// state acknowledges duplicates of data it has already taken.
//
// Data arriving *past* the stored acknowledgement -- beyond anything we took --
// is the other half of the same case: the peer writing after our FIN. libutp,
// still in CS_GOT_FIN, hands that to its application; we have none left to hand
// it to, so we drop it, but silently, which is what libutp's wire looks like.
// Either way the answer is never a RESET, and this returns true for both.
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
		// Data past the stored acknowledgement number is not a retransmission
		// of anything we took: it is the peer writing after our FIN, which is
		// what its half-close is for. We have nobody to give it to -- the
		// application closed -- but there is a difference between discarding
		// it and telling the peer its connection is broken.
		//
		// libutp discards. Its socket sits in CS_GOT_FIN and simply ignores
		// what arrives past the FIN; DEVIATIONS.md recorded us answering with
		// a RESET as a deliberate divergence, and measuring it showed what
		// that costs: a libutp peer writing after our FIN gets
		// UTP_ECONNRESET, three runs out of three. Silence matches the
		// reference and costs the peer nothing but a few retransmissions it
		// was going to make anyway.
		// The peer's FIN, numbered just past everything we took, is the end
		// of a stream we have whole: it is acknowledged, and the stored
		// acknowledgement moves to cover it. Silence here -- the rule for data
		// below -- left a peer that closed second retransmitting its FIN for
		// ever, each retransmission refreshing this entry; ending a closed
		// connection only once its FIN is acknowledged, as libutp does, made
		// that every such connection (review of 294cc2e).
		if pkt.Header.PacketType == st_fin && pkt.Header.SeqNum == ack.Header.AckNum+1 {
			finAck := *ack
			header := *ack.Header
			header.AckNum = pkt.Header.SeqNum
			finAck.Header = &header
			s.lingerAcks.put(cid.Hash(), &finAck)
			s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
			s.sendEvent(newOutgoingSocketEvent(&finAck, cid.Peer))
			return true
		}
		if wrappingLessThan(ack.Header.AckNum, pkt.Header.SeqNum) {
			if s.logger.Enabled(baseContext, slog.LevelDebug) {
				s.logger.Debug("ignoring data past a closed connection's FIN",
					"cid.send", cid.Send, "cid.recv", cid.Recv, "seq", pkt.Header.SeqNum)
			}
			// Refresh, as below: a peer still writing is still there, and
			// letting the entry expire under it would hand it the RESET this
			// exists to avoid. The cost is one entry, held only by a peer
			// that completed a handshake with us, which is the more expensive
			// half of the exchange.
			s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
			return true
		}
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("re-acking a FIN for a connection that has closed",
				"cid.send", cid.Send, "cid.recv", cid.Recv)
		}
		// Refresh the entry: a peer still retransmitting is still waiting.
		s.lingerExpirations.put(cid.Hash(), cid.Hash(), lingerAckTimeout)
		s.sendEvent(newOutgoingSocketEvent(ack, cid.Peer))
		return true
	}
	return false
}

func (s *UtpSocket) maybeSendReset(pkt *packet, peer ConnectionPeer) {
	key := rstInfoKey{
		peer:   peer.Hash(),
		connID: pkt.Header.ConnectionId,
		ackNr:  pkt.Header.SeqNum,
	}

	if _, seen := s.rstInfo.get(key); seen {
		// Already answered this one. Refresh its expiry and stay quiet, as
		// libutp does at utp_internal.cpp:2918.
		s.rstInfoExpirations.put(key, key, rstInfoTimeout)
		return
	}
	if s.rstInfo.len() > rstInfoLimit {
		// Past this point libutp stops answering entirely rather than let the
		// table grow without bound (utp_internal.cpp:2928).
		if s.logger.Enabled(baseContext, slog.LevelDebug) {
			s.logger.Debug("not sending RESET, too many already outstanding", "stored", s.rstInfo.len())
		}
		return
	}

	s.resetsSent.Add(1)
	s.rstInfo.put(key, struct{}{})
	s.rstInfoExpirations.put(key, key, rstInfoTimeout)

	// Fields match libutp's send_rst (utp_internal.cpp:846-863) exactly:
	//
	//   - ack_nr is the sequence number of the packet being rejected, so the
	//     peer can tell which of its packets drew the reset. We previously
	//     left it at zero, which named no packet at all.
	//   - seq_nr is random, so an off-path attacker cannot predict it.
	//   - windowsize is zero: there is no connection, so there is no receive
	//     window to advertise. We previously advertised 100000.
	//   - the timestamp fields are left at zero. libutp zeroes the whole
	//     header and never fills them in for a reset.
	randSeqNum := randomUint16()
	resetPacket := newPacketBuilder(st_reset, pkt.Header.ConnectionId, 0, 0, randSeqNum).
		WithAckNum(pkt.Header.SeqNum).
		Build()
	s.sendEvent(newOutgoingSocketEvent(resetPacket, peer))
}

// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can take the router's word for it instead of
// discovering the same limit by losing a probe.
//
// quoted is the *uTP datagram the ICMP message quoted* -- the original UDP
// payload -- not the ICMP packet and not the IP or UDP headers around it.
// peer is the address that datagram was sent to. nextHopMTU is the router's
// figure, which may be zero: older routers do not supply one, and the search
// falls back to halving the gap when it is missing or implausible.
//
// It reports whether the quoted bytes were recognised as a uTP packet
// belonging to a live connection, matching what libutp's return value means
// (utp_internal.cpp:3071-3072).
//
// This library does not open a raw socket to collect ICMP itself, and neither
// does libutp: on Linux the usual source is IP_RECVERR on the UDP socket, and
// feeding it in is the embedder's job.
func (s *UtpSocket) ProcessICMPFragmentation(quoted []byte, peer ConnectionPeer, nextHopMTU uint16) bool {
	return s.deliverICMP(quoted, peer, &icmpNotice{
		Kind:       icmpFragmentationNeeded,
		NextHopMTU: uint32(nextHopMTU),
	})
}

// ProcessICMPError reports an ICMP message that should tear a connection
// down: destination unreachable other than fragmentation-needed, time
// exceeded, parameter problem. The connection ends with ErrConnRefused if it
// had only sent its SYN and ErrReset otherwise, which is libutp's
// UTP_ECONNREFUSED / UTP_ECONNRESET distinction (utp_internal.cpp:3122).
//
// quoted and peer mean what they mean for ProcessICMPFragmentation, and the
// return value likewise. Deciding which ICMP messages are fatal is the
// caller's: libutp draws that line in its callers too.
func (s *UtpSocket) ProcessICMPError(quoted []byte, peer ConnectionPeer) bool {
	return s.deliverICMP(quoted, peer, &icmpNotice{Kind: icmpUnreachable})
}

// deliverICMP is libutp's parse_icmp_payload (utp_internal.cpp:3019-3067)
// followed by the delivery its two callers share.
func (s *UtpSocket) deliverICMP(quoted []byte, peer ConnectionPeer, notice *icmpNotice) bool {
	if peer == nil || notice == nil {
		return false
	}

	// "ICMP packets are only required to quote the first 8 bytes of the
	// layer4 payload. The UDP payload is 8 bytes, and the UTP header is
	// another 20 bytes. So, in order to find the entire UTP header, we need
	// the ICMP packet to quote 28 bytes." -- and then libutp checks for 20,
	// because by this point the UDP header is already off (:3032-3041).
	if len(quoted) < minimalHeaderSize {
		s.logger.Debug("ignoring ICMP: runt quoted payload", "len", len(quoted), "peer", peer)
		return false
	}
	// libutp reads the version and connection id straight out of the quoted
	// bytes and rejects anything that is not version 1 (:3043-3052). Its
	// UTP_Version also requires a known packet type and a first extension
	// below 3, which is exactly what this decoder checks. Only the fixed
	// header is parsed: a router that quoted 20 bytes has cut the extensions
	// off, and requiring them would throw away the report.
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
		// not correctness: the fragmentation case is rediscovered by the lost
		// probe and the error case by the retransmission timeout. Reported as
		// unrecognised would be wrong -- the connection was found.
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
