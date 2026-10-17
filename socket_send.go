package utp

import (
	"errors"
	"log/slog"
	"sync"
)

// connEventQueueLen is the capacity of a connection's event queue. Packets go
// through it only until the connection's event loop has finished setting up --
// after that the socket's reader hands them over inline (receiveInline) -- and
// a peer has nothing to send before then but a repeated SYN: its data waits
// for the SYN-ACK, which that setup sends. Otherwise it carries the
// application's close and half-close and ICMP reports, a few each. A packet
// that finds it full is dropped and counted (PacketsDroppedFullConnQueue).
//
// It was 1000, and a channel's buffer is allocated whole: 8 KB per
// connection, about a quarter of what an idle connection held once its
// receive buffer stopped being allocated up front.
const connEventQueueLen = 64

// encodeBufs holds the buffers outgoing packets are encoded into. A packet is
// written the moment it is encoded and the Conn may not keep it, so a buffer
// is free again once WriteTo returns. Encoding into a fresh slice was one
// allocation per packet sent, about a twelfth of all the allocations of a LAN
// transfer.
var encodeBufs = sync.Pool{New: func() any {
	b := make([]byte, 0, 1500)
	return &b
}}

// sendEvent carries out what a connection or the socket wants done on the
// wire, on the caller's goroutine: a packet written, or a finished
// connection's entry removed.
//
// This was a write loop on its own goroutine, fed by socketEvents, which put
// a hand-off between every decision to send and the send itself -- on an
// acknowledgement's path, the last of three. libutp's send callback writes
// from inside the call that decided to send (utp_call_sendto,
// utp_internal.cpp:713). Conn.WriteTo is called concurrently from here, as
// net.PacketConn allows.
func (s *UtpSocket) sendEvent(event *socketEvent) {
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("a socket event should be sent to target", "event.type", event.Type, "event.cid", event.ConnectionId)
	}
	switch event.Type {
	case outgoing:
		bufp := encodeBufs.Get().(*[]byte)
		encoded := event.Packet.appendEncoded((*bufp)[:0])
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("Send a packet out",
				"target.cid", event.ConnectionId,
				"packet.type", event.Packet.Header.PacketType.String(),
				"packet.seqNum", event.Packet.Header.SeqNum,
				"packet.ackNum", event.Packet.Header.AckNum,
				"packet.body.len", len(event.Packet.Body),
				"encoded.len", len(encoded))
		}
		var peer ConnectionPeer
		if cid, ok := event.ConnectionId.(*ConnectionId); ok {
			peer = cid.Peer
		} else {
			peer = event.ConnectionId
		}
		_, err := s.writeDatagram(encoded, peer, event.DontFragment)
		*bufp = encoded[:0]
		encodeBufs.Put(bufp)
		if err != nil {
			var eackEncodeLen int
			if event.Packet.Eack != nil {
				eackEncodeLen = event.Packet.Eack.EncodedLen()
			}
			if s.logger.Enabled(baseContext, LevelTrace) {
				s.logger.Trace("Failed to send uTP packet",
					"error", err,
					"cid", event.Packet.Header.ConnectionId,
					"type", event.Packet.Header.PacketType,
					"encoded.eack.len", eackEncodeLen,
					"encoded.len", len(encoded))
			}
		}

	case socketShutdown:
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("uTP conn shutdown", "cid.Hash", event.ConnectionId.Hash())
		}
		s.rememberLingerAck(event.ConnectionId.Hash(), event.Packet)
		s.removeConnStream(event.ConnectionId.Hash())
	}
}

// writeDatagram sends one datagram, with fragmentation disabled if it is an
// MTU probe and the Conn can do that.
//
// Falling back to an ordinary write is deliberate, and is what makes this
// safe to add. A Conn with no don't-fragment support probes exactly as it did
// before -- the search still runs, it just cannot tell a dropped probe from a
// fragmented one on IPv4. Failing the send instead would turn a missing
// socket option into a stalled connection.
//
// Datagrams are written from several goroutines -- the reader, each
// connection's event loop -- so a Conn that sets the bit as a socket option
// around one send has that send to itself: writeMu is held exclusively for it
// and shared by every other write. A Conn that cannot set the bit takes no
// lock at all.
func (s *UtpSocket) writeDatagram(b []byte, peer ConnectionPeer, dontFragment bool) (int, error) {
	w, dfw := s.socket.(DontFragmentWriter)
	if !dfw {
		return s.socket.WriteTo(b, peer)
	}
	if !dontFragment {
		s.writeMu.RLock()
		defer s.writeMu.RUnlock()
		return s.socket.WriteTo(b, peer)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := w.WriteToDontFragment(b, peer)
	if !errors.Is(err, ErrDontFragmentUnsupported) {
		return n, err
	}
	if s.logger.Enabled(baseContext, slog.LevelDebug) {
		s.logger.Debug("MTU probe sent without the don't-fragment bit; "+
			"this platform has no per-packet setting for it", "len", len(b))
	}
	return s.socket.WriteTo(b, peer)
}
