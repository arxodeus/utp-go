package utp_go

type StreamEventType int

const (
	streamIncoming StreamEventType = iota
	streamShutdown
	// streamCloseWrite wakes the event loop for a half-close. It is separate
	// from streamShutdown because that one means "finished entirely" and sets
	// the flag to prove it; CloseWrite only needs the loop to notice that the
	// sending side is done, and must not be mistaken for a full close.
	streamCloseWrite
	// streamICMP carries an ICMP report about a packet this connection sent.
	// The socket parses the quoted uTP header and finds the connection; the
	// connection decides what the report means, which is where libutp puts
	// the decision too (utp_process_icmp_fragmentation and
	// utp_process_icmp_error, utp_internal.cpp:3079-3150).
	streamICMP
)

// icmpKind distinguishes libutp's two ICMP entry points.
type icmpKind int

const (
	// icmpFragmentationNeeded is ICMP type 3 code 4, or ICMPv6 "packet too
	// big": the datagram was larger than some hop would carry.
	// utp_process_icmp_fragmentation.
	icmpFragmentationNeeded icmpKind = iota
	// icmpUnreachable is any ICMP error that should tear the connection
	// down. utp_process_icmp_error.
	icmpUnreachable
)

// icmpNotice is one ICMP report, already matched to a connection.
type icmpNotice struct {
	Kind icmpKind
	// NextHopMTU is the router's figure from a fragmentation-needed message:
	// the largest IP datagram the next hop will carry. Zero when the router
	// did not supply one, which older routers do not.
	NextHopMTU uint32
}

type socketEventType int

const (
	outgoing socketEventType = iota
	socketShutdown
)

type streamEvent struct {
	Type   StreamEventType
	Packet *packet
	// ICMP is set on streamICMP events and nil otherwise.
	ICMP *icmpNotice
}

// socketEvent represents events related to a socket.
type socketEvent struct {
	Type         socketEventType
	Packet       *packet
	ConnectionId ConnectionPeer
}

func newOutgoingSocketEvent(p *packet, cid ConnectionPeer) *socketEvent {
	return &socketEvent{
		Type:         outgoing,
		Packet:       p,
		ConnectionId: cid,
	}
}

// newShutdownSocketEvent tells the socket to forget a connection.
//
// lingerAck, when non-nil, is the acknowledgement to re-send if the peer keeps
// retransmitting its FIN after this connection has gone. Without it the socket
// answers that retransmission with a RESET, and a transfer that arrived whole
// ends as a connection reset for the peer. See UtpSocket.rememberLingerAck.
func newShutdownSocketEvent(cid ConnectionPeer, lingerAck *packet) *socketEvent {
	return &socketEvent{
		Type:         socketShutdown,
		Packet:       lingerAck,
		ConnectionId: cid,
	}
}
