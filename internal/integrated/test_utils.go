package integrated

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	utp "github.com/arxodeus/utp"
)

var (
	ErrChClosed      = errors.New("channel closed")
	ErrBufferToSmall = errors.New("buffer too small for perfect link")
)

type LinkDecider interface {
	shouldSend() bool
}

type ManualLinkDecider struct {
	upSwitch atomic.Bool
}

func newManualLinkDecider() *ManualLinkDecider {
	decider := &ManualLinkDecider{}
	decider.upSwitch.Store(true)
	return decider
}

func (d *ManualLinkDecider) shouldSend() bool {
	return d.upSwitch.Load()
}

// DropFirstNSent drops the first targetDropsN datagrams. Safe for
// concurrent use: a socket writes from more than one goroutine.
type DropFirstNSent struct {
	targetDropsN int64
	curDropsN    atomic.Int64
}

func newDropFirstNSent(dropsN int) *DropFirstNSent {
	return &DropFirstNSent{targetDropsN: int64(dropsN)}
}

func (d *DropFirstNSent) shouldSend() bool {
	return d.curDropsN.Add(1) > d.targetDropsN
}

type MockConnectedPeer struct {
	name string
}

func (p *MockConnectedPeer) Hash() string {
	return p.name
}

type MockUDPSocket struct {
	sendCh      chan []byte
	recvCh      chan []byte
	onlyPeer    utp.ConnectionPeer
	linkDecider LinkDecider
}

func (s *MockUDPSocket) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	buf, ok := <-s.recvCh
	if !ok {
		return 0, nil, ErrChClosed
	}
	n := len(buf)
	if len(b) < len(buf) {
		return 0, nil, ErrChClosed
	}
	slog.Debug("read a raw packet from mocksocket", "from", s.onlyPeer, "len(buf)", len(buf), "buf", hex.EncodeToString(buf))
	copy(b[:n], buf)
	return n, s.onlyPeer, nil
}

// Queued implements utp.QueuedReader, as a real socket's batched read would.
func (s *MockUDPSocket) Queued() int {
	return len(s.recvCh)
}

func (s *MockUDPSocket) WriteTo(b []byte, dst utp.ConnectionPeer) (int, error) {
	if dst.Hash() != s.onlyPeer.Hash() {
		panic(fmt.Sprintf("MockUdpSocket only supports Writing To one peer: dst.peer = %s, onlyPeer = %s", dst.Hash(), s.onlyPeer.Hash()))
	}
	if !s.linkDecider.shouldSend() {
		slog.Debug("Dropping packet", "dst.peer", dst.Hash(), "onlyPeer", s.onlyPeer.Hash())
		return len(b), nil
	}
	// Like a UDP socket, never wait for the peer: a datagram that finds the
	// link's queue full is lost. The socket writes from its reader, as
	// libutp's embedder does, so a WriteTo that waited for the other side to
	// read would let two sockets each wait on the other for ever.
	// Copied, as a UDP socket copies: the caller reuses b once this returns.
	b = append([]byte(nil), b...)
	select {
	case s.sendCh <- b:
	default:
		slog.Debug("link queue full, dropping packet", "dst.peer", dst.Hash())
		return len(b), nil
	}
	slog.Debug("Sent a packet out to dest", "dest.peer", dst, "len", len(b), "data", hex.EncodeToString(b))
	return len(b), nil
}
func (s *MockUDPSocket) Close() error {
	return nil
}

// linkQueue is how many datagrams a mock link holds before it drops: more
// than any window the tests here open, so a drop is what a LinkDecider asks
// for and not an accident of the harness.
const linkQueue = 8192

func buildLinkPair(aDecider LinkDecider, bDecider LinkDecider) (*MockUDPSocket, *MockUDPSocket) {
	peerA, peerB := &MockConnectedPeer{name: "peerA"}, &MockConnectedPeer{name: "peerB"}
	peerACh, peerBCh := make(chan []byte, linkQueue), make(chan []byte, linkQueue)

	// A -> B
	a, b := &MockUDPSocket{
		sendCh:      peerACh,
		recvCh:      peerBCh,
		onlyPeer:    peerB,
		linkDecider: aDecider,
	}, &MockUDPSocket{
		// B -> A
		sendCh:      peerBCh,
		recvCh:      peerACh,
		onlyPeer:    peerA,
		linkDecider: bDecider,
	}
	return a, b
}

func buildCidPair(socketA *MockUDPSocket, socketB *MockUDPSocket, lowerID uint16) (*utp.ConnectionID, *utp.ConnectionID) {
	higherID := lowerID + 1
	cidA, cidB := utp.NewConnectionID(socketA.onlyPeer, lowerID, higherID), utp.NewConnectionID(socketB.onlyPeer, higherID, lowerID)
	return cidA, cidB
}

func buildConnectedPair() (*MockUDPSocket, *utp.ConnectionID, *MockUDPSocket, *utp.ConnectionID) {
	socketA, socketB := buildLinkPair(newManualLinkDecider(), newManualLinkDecider())
	cidA, cidB := buildCidPair(socketA, socketB, 100)
	return socketA, cidA, socketB, cidB
}

func buildLinkDropSentPair(n int) (*MockUDPSocket, *utp.ConnectionID, *MockUDPSocket, *utp.ConnectionID) {
	socketA, socketB := buildLinkPair(newManualLinkDecider(), newDropFirstNSent(n))
	cidA, cidB := buildCidPair(socketA, socketB, 100)
	return socketA, cidA, socketB, cidB
}
