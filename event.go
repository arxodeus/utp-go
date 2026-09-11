package utp_go

type StreamEventType int

const (
	streamIncoming StreamEventType = iota
	streamShutdown
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
