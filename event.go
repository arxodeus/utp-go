package utp

type streamEventType int

const (
	streamIncoming streamEventType = iota
	streamShutdown
	// streamCloseWrite wakes the event loop for a half-close of the sending
	// side. It is distinct from streamShutdown, which closes both sides.
	streamCloseWrite
	// streamCloseRead wakes the event loop for a half-close of the
	// receiving side.
	streamCloseRead
	// streamICMP carries an ICMP report about a packet this connection sent.
	// The socket finds the connection from the quoted header; the
	// connection decides what the report means.
	streamICMP
)

// icmpKind is the kind of an ICMP report.
type icmpKind int

const (
	// icmpFragmentationNeeded is ICMP type 3 code 4, or ICMPv6 "packet too
	// big": the datagram was larger than a hop would carry.
	icmpFragmentationNeeded icmpKind = iota
	// icmpUnreachable is any ICMP error that ends the connection.
	icmpUnreachable
)

// icmpNotice is one ICMP report, already matched to a connection.
type icmpNotice struct {
	Kind icmpKind
	// NextHopMTU is the largest IP datagram the next hop will carry, from a
	// fragmentation-needed message, or zero if the router did not supply it.
	NextHopMTU uint32
}

type socketEventType int

const (
	outgoing socketEventType = iota
	socketShutdown
)

type streamEvent struct {
	Type   streamEventType
	Packet *packet
	// ICMP is set on streamICMP events and nil otherwise.
	ICMP *icmpNotice
}

// socketEvent is a request from a connection to its socket.
type socketEvent struct {
	Type         socketEventType
	Packet       *packet
	ConnectionId ConnectionPeer
	// DontFragment marks the datagram as an MTU probe, to be sent with
	// fragmentation disabled if the Conn supports it. See
	// DontFragmentWriter.
	DontFragment bool
}

func newOutgoingSocketEvent(p *packet, cid ConnectionPeer) *socketEvent {
	return &socketEvent{
		Type:         outgoing,
		Packet:       p,
		ConnectionId: cid,
	}
}

// newOutgoingProbeSocketEvent is newOutgoingSocketEvent for an MTU probe.
func newOutgoingProbeSocketEvent(p *packet, cid ConnectionPeer) *socketEvent {
	return &socketEvent{
		Type:         outgoing,
		Packet:       p,
		ConnectionId: cid,
		DontFragment: true,
	}
}

// newShutdownSocketEvent tells the socket to forget a connection.
//
// lingerAck, if non-nil, is the acknowledgement the socket sends if the peer
// retransmits its FIN after the connection has gone, instead of a RESET.
func newShutdownSocketEvent(cid ConnectionPeer, lingerAck *packet) *socketEvent {
	return &socketEvent{
		Type:         socketShutdown,
		Packet:       lingerAck,
		ConnectionId: cid,
	}
}
