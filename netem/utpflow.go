package netem

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

// UtpPair is two uTP sockets joined by an emulated network, ready to carry
// flows. It is the thing M5's gates are run against.
type UtpPair struct {
	Net          *Network
	A, B         *Endpoint
	SockA, SockB *utp.UtpSocket
}

// NewUtpPair wraps two endpoints in uTP sockets. Close the Network to shut
// everything down.
func NewUtpPair(ctx context.Context, n *Network, a, b *Endpoint, logger log.Logger) *UtpPair {
	return &UtpPair{
		Net:   n,
		A:     a,
		B:     b,
		SockA: utp.WithSocket(ctx, a, logger),
		SockB: utp.WithSocket(ctx, b, logger),
	}
}

// TransferResult is what one flow achieved.
type TransferResult struct {
	// BytesTransferred is what the receiver actually read.
	BytesTransferred int
	// Elapsed is wall-clock time from connect to end of stream.
	Elapsed time.Duration
	// Goodput is application-level throughput: payload delivered per second,
	// excluding headers and retransmissions. This is the number that matters
	// -- a flow can be busy and still deliver nothing.
	Goodput Throughput
	// Sender and Receiver hold the recorded metric series for each side.
	Sender   *Recorder
	Receiver *Recorder
	// Verified reports whether the received bytes matched what was sent.
	Verified bool
}

// String renders the result for a test log.
func (r *TransferResult) String() string {
	return fmt.Sprintf("%d bytes in %v, goodput %s, verified=%t",
		r.BytesTransferred, r.Elapsed.Round(time.Millisecond), r.Goodput, r.Verified)
}

// FlowOptions configures one transfer.
type FlowOptions struct {
	// InitiatorCid is the lower of the two connection ids. Give each
	// concurrent flow a distinct value at least 2 apart.
	InitiatorCid uint16
	// Config, if non-nil, is used as the base connection configuration.
	// Its Metrics observer is replaced.
	Config *utp.ConnectionConfig
	// MetricsInterval controls sampling density.
	MetricsInterval time.Duration
	// Verify compares received bytes against what was sent. Costs a full
	// comparison; leave off for long soaks.
	Verify bool
}

// RunTransfer sends data from the pair's A side to its B side and waits for
// the receiver to read it to end of stream.
//
// A and B here are the *directions of data flow*, not the endpoints' names:
// the sender initiates, the receiver accepts.
func (p *UtpPair) RunTransfer(ctx context.Context, data []byte, opts FlowOptions) (*TransferResult, error) {
	return runFlow(ctx, p.SockA, p.A, p.SockB, p.B, data, opts)
}

// RunTransferBToA is RunTransfer in the opposite direction.
func (p *UtpPair) RunTransferBToA(ctx context.Context, data []byte, opts FlowOptions) (*TransferResult, error) {
	return runFlow(ctx, p.SockB, p.B, p.SockA, p.A, data, opts)
}

func runFlow(
	ctx context.Context,
	senderSock *utp.UtpSocket, senderEp *Endpoint,
	receiverSock *utp.UtpSocket, receiverEp *Endpoint,
	data []byte,
	opts FlowOptions,
) (*TransferResult, error) {
	initiatorCid := opts.InitiatorCid
	if initiatorCid == 0 {
		initiatorCid = 100
	}
	responderCid := initiatorCid + 1

	senderRec := NewRecorder()
	receiverRec := NewRecorder()

	mkConfig := func(rec *Recorder) *utp.ConnectionConfig {
		cfg := utp.NewConnectionConfig()
		if opts.Config != nil {
			c := *opts.Config
			cfg = &c
		}
		cfg.Metrics = rec.Observe
		if opts.MetricsInterval > 0 {
			cfg.MetricsInterval = opts.MetricsInterval
		}
		return cfg
	}

	// Follow the same cid convention as the rest of the package: the SYN
	// carries the initiator id, and the acceptor derives the responder id
	// from it.
	acceptCid := utp.NewConnectionId(senderEp.Addr(), responderCid, initiatorCid)
	connectCid := utp.NewConnectionId(receiverEp.Addr(), initiatorCid, responderCid)

	var (
		wg       sync.WaitGroup
		recvErr  error
		sendErr  error
		received []byte
		start    time.Time
		elapsed  time.Duration
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := receiverSock.AcceptWithCid(ctx, acceptCid, mkConfig(receiverRec))
		if err != nil {
			recvErr = fmt.Errorf("accept: %w", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(data))
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			recvErr = fmt.Errorf("read: %w", err)
			return
		}
		received = buf[:min(n, len(buf))]
		elapsed = time.Since(start)
	}()

	start = time.Now()
	go func() {
		defer wg.Done()
		stream, err := senderSock.ConnectWithCid(ctx, connectCid, mkConfig(senderRec))
		if err != nil {
			sendErr = fmt.Errorf("connect: %w", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, data); err != nil {
			sendErr = fmt.Errorf("write: %w", err)
		}
	}()

	wg.Wait()
	if sendErr != nil {
		return nil, sendErr
	}
	if recvErr != nil {
		return nil, recvErr
	}
	if elapsed == 0 {
		elapsed = time.Since(start)
	}

	res := &TransferResult{
		BytesTransferred: len(received),
		Elapsed:          elapsed,
		Goodput:          Throughput{Bytes: uint64(len(received)), Duration: elapsed},
		Sender:           senderRec,
		Receiver:         receiverRec,
	}
	if opts.Verify {
		res.Verified = len(received) == len(data) && string(received) == string(data)
	} else {
		res.Verified = len(received) == len(data)
	}
	return res, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
