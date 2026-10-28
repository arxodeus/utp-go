package utp

import (
	"errors"
	"log/slog"
	"sync"
)

// connEventQueueLen is the capacity of a connection's event queue. Packets
// pass through it only until the connection's event loop is set up; after
// that the reader delivers them inline (receiveInline). It otherwise carries
// close, half-close and ICMP reports. A packet that finds it full is dropped
// and counted in PacketsDroppedFullConnQueue. The buffer is allocated whole,
// so it is kept small.
const connEventQueueLen = 64

// encodeBufs holds the buffers outgoing packets are encoded into. A packet is
// written as soon as it is encoded and the Conn may not retain it, so the
// buffer is free again once WriteTo returns.
var encodeBufs = sync.Pool{New: func() any {
	b := make([]byte, 0, 1500)
	return &b
}}

// sendEvent performs a connection's or the socket's request on the caller's
// goroutine: it writes a packet, or removes a finished connection's entry.
// Writing from the goroutine that decided to send avoids a hand-off on every
// send. Conn.WriteTo may be called concurrently, as net.PacketConn allows.
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
// MTU probe and the Conn supports it. Otherwise the probe is sent as an
// ordinary datagram: the MTU search still runs, but on IPv4 it cannot tell a
// dropped probe from a fragmented one.
//
// A Conn that sets don't-fragment as a socket option around one send holds
// writeMu exclusively for it; every other write holds it shared. A Conn
// without don't-fragment support takes no lock.
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
