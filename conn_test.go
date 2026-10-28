package utp

import (
	"context"
	"math"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	TEST_BUFFER_SIZE     = 2048
	TEST_DELAYtime       = time.Millisecond * 100
	TEST_INITIAL_TIMEOUT = time.Second
)

func CreateTestConnection(endpoint endpointInfo) *connection {
	// Create the channels.
	socketEvents := make(chan *socketEvent, 100)
	reads := make(chan *readOrWriteResult, 100)

	// Create the peer address.
	peer := &tcpPeer{&net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 3400,
	},
	}

	// Create the connection id.
	cid := ConnectionId{
		Send: 101,
		Recv: 100,
		Peer: peer,
	}

	unackTimeoutCh := make(chan *packet, 1000)
	timers := newRetransmitTimers(TEST_INITIAL_TIMEOUT/4, 8)
	conn := &connection{
		logger:         newLogger(nil),
		state:          newConnState(make(chan error, 1)),
		cid:            &cid,
		config:         NewConnectionConfig(),
		endpoint:       &endpoint,
		peerTsDiff:     100 * time.Millisecond,
		peerRecvWindow: math.MaxUint32,
		socketEvents:   socketEvents,
		timers:         timers,
		timerScope:     timers.newScope(),
		armed:          make(map[uint16]struct{}),
		unackTimeoutCh: unackTimeoutCh,
		ctx:            context.Background(),
		reads:          reads,
		readable:       make(chan struct{}, 1),
		pendingWrites:  make([]*queuedWrite, 0),
		writable:       make(chan struct{}, 1),
		latestTimeout:  nil,
		// As newConnection sets it.
		mtu: newMtuSearch(uint32(NewConnectionConfig().MaxPacketSize), time.Now()),
	}

	return conn
}

func TestOnSynInitiator(t *testing.T) {
	syn := uint16(100)
	endpoint := endpointInfo{
		Type:   initiator,
		SynNum: syn,
	}
	conn := CreateTestConnection(endpoint)
	before := conn.state.stateType

	// A SYN at a connection we dialed is not ours to answer, and is ignored.
	conn.onPacket(newPacketBuilder(st_syn, 7, 0, 1<<20, syn).Build(), time.Now())

	require.Equal(t, before, conn.state.stateType, "a stray SYN changed the connection's state")
	require.NoError(t, conn.state.Err, "a stray SYN ended the connection")
}

func TestOnSynAcceptor(t *testing.T) {
	// Setup
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}

	conn := CreateTestConnection(endpoint)

	conn.onSyn(syn)
	require.Equal(t, connConnecting, conn.state.stateType, "expected state to be connecting, got %v", conn.state.stateType)
}

func TestOnSynAcceptorNonMatchingSyn(t *testing.T) {
	// Step 1: Setup the connection with expected syn
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}
	conn := CreateTestConnection(endpoint)

	before := conn.state.stateType

	// Step 2: a SYN with a different sequence number belongs to another
	// connection that drew the same ids. It is ignored.
	altSyn := uint16(128)
	conn.onPacket(newPacketBuilder(st_syn, 7, 0, 1<<20, altSyn).Build(), time.Now())
	require.Equal(t, before, conn.state.stateType,
		"a SYN that was not this connection's changed its state")
	require.NoError(t, conn.state.Err, "a SYN that was not this connection's ended it")
}

func TestOnStateConnectingInitiator(t *testing.T) {
	// Setup
	syn := uint16(100)
	endpoint := endpointInfo{
		Type:   initiator,
		SynNum: syn,
	}
	conn := CreateTestConnection(endpoint)

	// Test
	seqNum := uint16(1)
	conn.onState(seqNum, syn)

	// Verify
	require.Equal(t, connConnected, conn.state.stateType,
		"expected state to be connected, got %v", conn.state.stateType)
	require.Nil(t, conn.state.closing, "expected closing to be nil")
}

func TestOnPacketInvalidAckNum(t *testing.T) {
	// Setup connection
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}
	conn := CreateTestConnection(endpoint)

	// Setup congestion control
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)

	// Send a packet
	data := []byte{0xef}
	length := uint32(64)
	now := time.Now()
	sentPackets.OnTransmit(
		synAck+1,
		st_data,
		data,
		length,
		now,
	)

	// Setup buffers and state
	sendBuf := newSendBuffer(TEST_BUFFER_SIZE)
	recvBuf := newReceiveBuffer(TEST_BUFFER_SIZE, syn)

	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: sentPackets,
		SendBuf:     sendBuf,
		RecvBuf:     recvBuf,
	}

	// An acknowledgement number past everything we have sent acknowledges
	// nothing, and leaves the connection alone: one forged or late
	// ST_STATE must not end a transfer.
	delay := 100 * time.Millisecond
	ackResult := conn.processAck(synAck+2, nil, delay, now)

	require.NoError(t, ackResult, "an acknowledgement outside the window is ignored, not an error")
	require.Equal(t, connConnected, conn.state.stateType,
		"the connection must survive an acknowledgement it cannot match")
	require.NoError(t, conn.state.Err, "no error should have been recorded")
	require.True(t, conn.state.SentPackets.HasUnackedPackets(),
		"the outstanding packet must still be outstanding: nothing was acknowledged")
}

func TestOnFinEstablished(t *testing.T) {
	// Setup
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}
	conn := CreateTestConnection(endpoint)

	// Create buffers and controllers
	sendBuf := newSendBuffer(TEST_BUFFER_SIZE)
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	recvBuf := newReceiveBuffer(TEST_BUFFER_SIZE, syn)

	// Set connected state
	conn.state = &connState{
		stateType:   connConnected,
		SendBuf:     sendBuf,
		SentPackets: sentPackets,
		RecvBuf:     recvBuf,
	}

	// Test FIN
	fin := syn + 3 // wrapping add
	err := conn.onFin(fin, []byte{})
	require.NoError(t, err, "expected no error")

	// Verify state
	require.Equal(t, connConnected, conn.state.stateType,
		"expected state to be Connected, got %v", conn.state.stateType)
	require.NotNil(t, conn.state.closing, "expected closing to be not nil")
	require.Nil(t, conn.state.closing.LocalFin, "expected closing.LocalFin to be nil")
	require.Equal(t, fin, *conn.state.closing.RemoteFin,
		"expected closing.RemoteFin to be %d, got %d", fin, *conn.state.closing.RemoteFin)
}

func TestOnFinClosing(t *testing.T) {
	// Step 1: Setup initial connection
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}
	conn := CreateTestConnection(endpoint)

	// Step 2: Setup buffers and controllers
	sendBuf := newSendBuffer(TEST_BUFFER_SIZE)
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	recvBuf := newReceiveBuffer(TEST_BUFFER_SIZE, syn)

	// Step 3: Set local fin
	localFin := synAck + 3
	conn.state = &connState{
		stateType:   connConnected,
		SendBuf:     sendBuf,
		SentPackets: sentPackets,
		RecvBuf:     recvBuf,
		closing: &closingRecord{
			LocalFin:  &localFin,
			RemoteFin: nil,
		},
	}

	// Step 4: Test remote fin
	remoteFin := syn + 3
	err := conn.onFin(remoteFin, []byte{})
	require.NoError(t, err, "expected no error")

	// Step 5: Verify state
	require.Equal(t, connConnected, conn.state.stateType,
		"expected state to be connected, got %v", conn.state.stateType)
	require.NotNil(t, conn.state.closing, "expected closing to be not nil")
	require.NotNil(t, conn.state.closing.LocalFin, "expected closing.LocalFin to be not nil")
	require.Equal(t, localFin, *conn.state.closing.LocalFin,
		"expected closing.LocalFin to be %d, got %d", localFin, *conn.state.closing.LocalFin)
	require.NotNil(t, conn.state.closing.RemoteFin, "expected closing.RemoteFin to be not nil")
	require.Equal(t, remoteFin, *conn.state.closing.RemoteFin,
		"expected closing.LocalFin to be %d, got %d", remoteFin, *conn.state.closing.RemoteFin)
}

func TestOnFinClosingNonMatchingFin(t *testing.T) {
	// Step 1: Setup connection
	syn := uint16(100)
	synAck := uint16(101)
	endpoint := endpointInfo{
		Type:   acceptor,
		SynNum: syn,
		SynAck: synAck,
	}
	conn := CreateTestConnection(endpoint)

	// Step 2: Setup buffers
	sendBuf := newSendBuffer(TEST_BUFFER_SIZE)
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	recvBuf := newReceiveBuffer(TEST_BUFFER_SIZE, syn)

	// Step 3: Set initial fin
	fin := syn + 3
	conn.state = &connState{
		stateType:   connConnected,
		SendBuf:     sendBuf,
		SentPackets: sentPackets,
		RecvBuf:     recvBuf,
		closing: &closingRecord{
			LocalFin:  nil,
			RemoteFin: &fin,
		},
	}

	// Step 4: Test with different fin
	altFin := fin + 1
	err := conn.onFin(altFin, []byte{})
	require.NoError(t, err, "expected no error")

	// Step 5: the end of the stream is still the first FIN's, and the
	// connection continues. TestConformanceSecondFinDoesNotMoveTheEnd
	// compares this with the reference implementation.
	require.Equal(t, connConnected, conn.state.stateType, "expected the connection to stay up")
	require.NoError(t, conn.state.Err)
	require.Equal(t, fin, *conn.state.closing.RemoteFin, "the end of the stream moved")
	require.True(t, conn.dropUnacked, "a FIN numbered past the end should be dropped unacknowledged")
}

func TestOnResetNonClosed(t *testing.T) {
	// Setup
	syn := uint16(100)
	endpoint := endpointInfo{
		Type:   initiator,
		SynNum: syn,
	}
	conn := CreateTestConnection(endpoint)

	// A reset answering our SYN: the dial was refused.
	require.Equal(t, connConnecting, conn.state.stateType)
	conn.onReset()
	require.Equal(t, connClosed, conn.state.stateType, "expected state to be closed")
	require.ErrorIs(t, conn.state.Err, ErrConnRefused,
		"a reset answering our SYN is a refusal; libutp means to say so and cannot (utp_internal.cpp:2865-2870)")

	// Once connected, a reset is a reset.
	conn = CreateTestConnection(endpoint)
	conn.state.stateType = connConnected
	conn.onReset()
	require.ErrorIs(t, conn.state.Err, ErrReset, "expected error %v, got %v", ErrReset, conn.state.Err)
}

func TestOnResetClosed(t *testing.T) {
	// Setup
	syn := uint16(100)
	endpoint := endpointInfo{
		Type:   initiator,
		SynNum: syn,
	}
	conn := CreateTestConnection(endpoint)

	// Set initial closed state
	conn.state = &connState{
		stateType: connClosed,
	}

	// Test reset
	conn.onReset()

	// Verify state remains unchanged
	require.Equal(t, connClosed, conn.state.stateType, "expected state to be closed")
	require.NoError(t, conn.state.Err, "expected error to be nil")
}
