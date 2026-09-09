//go:build cgo

package netem

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// Real libutp, running as an endpoint on the emulated network.
//
// Everything in BENCHMARKS.md compares this library against a previous version
// of itself, and every congestion-control row in COMPATIBILITY.md is *cited* —
// code read against utp_internal.cpp and matched by hand — because libutp had
// never been run over the emulated network. This is what closes that gap: the
// same links, the same payload, the same measurements, with the reference
// implementation at one end.
//
// libutp.Driver is deliberately inert: no socket, no clock, no threads. That
// is what makes this possible. The loop below supplies all three — it hands
// the driver packets from a netem Endpoint, moves its clock to match the real
// one the rest of the harness runs on, and writes whatever it emits back to
// the link.

// libutpTickInterval is how often the loop advances libutp's clock and calls
// utp_check_timeouts when no packet has arrived.
//
// libutp rate-limits itself internally to TIMEOUT_CHECK_INTERVAL, 500ms
// (utp_internal.cpp:37, :3284-3285), so calling more often costs a cgo call
// and changes nothing. It is this fine so that the *clock* is accurate: every
// timestamp libutp puts on a packet, and every delay sample it takes from one,
// is only as good as the last SetTime.
const libutpTickInterval = 200 * time.Microsecond

// libutpRole is which end of the transfer libutp plays.
type libutpRole int

const (
	libutpSender libutpRole = iota
	libutpReceiver
)

// libutpEndpoint drives a libutp.Driver over a netem Endpoint.
//
// The driver is not safe for concurrent use, so exactly one goroutine — run —
// ever touches it. Packets arrive through inbox from a reader goroutine, and
// results are read only after run has returned.
type libutpEndpoint struct {
	drv   *libutp.Driver
	ep    *Endpoint
	dst   utp.ConnectionPeer
	inbox chan []byte
	start time.Time

	role    libutpRole
	payload []byte // what a sender sends
	expect  int    // how many bytes a receiver waits for

	received []byte
	emitted  uint64
}

// newLibutpEndpoint pins libutp's random source to connSeed, which fixes the
// connection ids it uses: a libutp initiator takes recv_id = connSeed and
// send_id = connSeed + 1, so the other end must accept on the mirror of that.
func newLibutpEndpoint(ep *Endpoint, dst utp.ConnectionPeer, connSeed uint32, role libutpRole) (*libutpEndpoint, error) {
	// libutp's clock is set to the same base this library stamps packets with,
	// time.Now().UnixMicro() (conn.go:1245). It has to be: the two ends
	// exchange timestamps and each computes the other's queueing delay by
	// subtracting them, so a driver whose clock starts at zero hands libutp a
	// delay reading that is really the offset between two unrelated epochs.
	drv, err := libutp.NewDriver(uint64(time.Now().UnixMicro()))
	if err != nil {
		return nil, err
	}
	drv.PushRandom(connSeed)
	return &libutpEndpoint{
		drv:   drv,
		ep:    ep,
		dst:   dst,
		inbox: make(chan []byte, 4096),
		role:  role,
	}, nil
}

func (l *libutpEndpoint) Close() { l.drv.Close() }

// readLoop moves packets off the link into inbox. It is the only goroutine
// besides run, and it never touches the driver.
func (l *libutpEndpoint) readLoop(ctx context.Context) {
	buf := make([]byte, 65536)
	for {
		n, _, err := l.ep.ReadFrom(buf)
		if err != nil {
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		select {
		case l.inbox <- pkt:
		case <-ctx.Done():
			return
		}
	}
}

// run drives the transfer to completion and returns how long it took, measured
// from the moment before the SYN is emitted to the moment the last byte is in
// hand.
func (l *libutpEndpoint) run(ctx context.Context) (time.Duration, error) {
	l.start = time.Now()
	go l.readLoop(ctx)

	switch l.role {
	case libutpSender:
		if err := l.drv.Connect(); err != nil {
			return 0, fmt.Errorf("libutp connect: %w", err)
		}
		// The driver buffers the whole payload and feeds libutp as its send
		// window allows (drv_pump_writes, driver.cpp:87-97), pumping again on
		// every inject and timeout check. CloseStream only reaches utp_close
		// once that buffer has drained, so queueing both up front is safe.
		if _, err := l.drv.Write(l.payload); err != nil {
			return 0, fmt.Errorf("libutp write: %w", err)
		}
		l.drv.CloseStream()
	case libutpReceiver:
		l.drv.Listen()
	}
	l.flush()

	tick := time.NewTicker(libutpTickInterval)
	defer tick.Stop()
	readBuf := make([]byte, 64*1024)

	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("libutp %s: %w (received %d bytes)",
				l.role, ctx.Err(), len(l.received))

		case pkt := <-l.inbox:
			l.setTime()
			l.drv.Inject(pkt)
			// Drain whatever else has already arrived into the same batch, so
			// libutp gets the batched delivery its embedder API assumes and
			// its deferred acks mean what they mean in production.
			for i := 0; i < 64; i++ {
				select {
				case more := <-l.inbox:
					l.drv.Inject(more)
					continue
				default:
				}
				break
			}
			l.drv.IssueAcks()

		case <-tick.C:
			l.setTime()
			l.drv.CheckTimeouts()
		}

		// Drain received data every pass. libutp advertises its window from
		// what is still unread (drv_get_read_buffer_size, driver.cpp:275-278),
		// so a receiver that does not read closes its own window.
		for {
			n := l.drv.Read(readBuf)
			if n == 0 {
				break
			}
			l.received = append(l.received, readBuf[:n]...)
		}
		l.flush()

		if done, err := l.finished(); done || err != nil {
			return time.Since(l.start), err
		}
	}
}

// finished reports whether this end has nothing left to do.
func (l *libutpEndpoint) finished() (bool, error) {
	switch l.role {
	case libutpReceiver:
		if len(l.received) >= l.expect {
			return true, nil
		}
	case libutpSender:
		// The sender is done when libutp has torn the connection down, which
		// happens after the FIN it queued in CloseStream is acknowledged.
		if st := l.drv.State(); st == libutp.StateDestroyed {
			return true, nil
		}
	}
	if st := l.drv.State(); st == libutp.StateError {
		return true, errors.New("libutp reported a connection error")
	}
	return false, nil
}

func (l *libutpEndpoint) setTime() {
	l.drv.SetTime(uint64(time.Now().UnixMicro()))
}

// flush writes everything libutp has emitted onto the link.
func (l *libutpEndpoint) flush() {
	pkts := l.drv.Emitted()
	if len(pkts) == 0 {
		return
	}
	l.drv.ClearEmitted()
	for _, pkt := range pkts {
		if _, err := l.ep.WriteTo(pkt, l.dst); err != nil {
			return
		}
		l.emitted++
	}
}

func (r libutpRole) String() string {
	if r == libutpSender {
		return "sender"
	}
	return "receiver"
}

// --- flows joining libutp to this library -----------------------------------

// libutpToGo runs a transfer with real libutp sending and this library
// receiving. It returns the elapsed time and the bytes the receiver read.
func libutpToGo(ctx context.Context, n *Network, from, to *Endpoint, payload []byte, connSeed uint32) (time.Duration, []byte, error) {
	sender, err := newLibutpEndpoint(from, to.Addr(), connSeed, libutpSender)
	if err != nil {
		return 0, nil, err
	}
	defer sender.Close()
	sender.payload = payload

	sock := utp.WithSocket(ctx, to, quiet())
	defer sock.Close()

	// A libutp initiator puts its recv_id in the SYN, so our acceptor mirrors
	// it: send on what arrived, receive on one more.
	cid := utp.NewConnectionId(from.Addr(), uint16(connSeed)+1, uint16(connSeed))

	var (
		wg       sync.WaitGroup
		got      []byte
		acceptEr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := sock.AcceptWithCid(ctx, cid, utp.NewConnectionConfig())
		if err != nil {
			acceptEr = fmt.Errorf("accept from libutp: %w", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		nRead, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && !errors.Is(err, context.Canceled) {
			acceptEr = fmt.Errorf("read from libutp: %w", err)
			return
		}
		if nRead > len(buf) {
			nRead = len(buf)
		}
		got = buf[:nRead]
	}()

	elapsed, runErr := sender.run(ctx)
	wg.Wait()
	if runErr != nil {
		return 0, nil, runErr
	}
	if acceptEr != nil {
		return 0, nil, acceptEr
	}
	return elapsed, got, nil
}

// goToLibutp runs a transfer with this library sending and real libutp
// receiving.
func goToLibutp(ctx context.Context, n *Network, from, to *Endpoint, payload []byte, connSeed uint32) (time.Duration, []byte, error) {
	receiver, err := newLibutpEndpoint(to, from.Addr(), connSeed, libutpReceiver)
	if err != nil {
		return 0, nil, err
	}
	defer receiver.Close()
	receiver.expect = len(payload)

	sock := utp.WithSocket(ctx, from, quiet())
	defer sock.Close()

	// libutp is listening and will take whatever ids the SYN carries.
	cid := utp.NewConnectionId(to.Addr(), uint16(connSeed), uint16(connSeed)+1)

	var (
		wg      sync.WaitGroup
		sendErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := sock.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
		if err != nil {
			sendErr = fmt.Errorf("connect to libutp: %w", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, payload); err != nil {
			sendErr = fmt.Errorf("write to libutp: %w", err)
		}
	}()

	elapsed, runErr := receiver.run(ctx)
	wg.Wait()
	if runErr != nil {
		return 0, nil, runErr
	}
	if sendErr != nil {
		return 0, nil, sendErr
	}
	return elapsed, receiver.received, nil
}
