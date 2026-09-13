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
)

type socketEventType int

const (
	outgoing socketEventType = iota
	socketShutdown
)

type streamEvent struct {
	Type   StreamEventType
	Packet *packet
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
