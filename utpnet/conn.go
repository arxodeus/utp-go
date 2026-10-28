package utpnet

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	utp "github.com/arxodeus/utp"
)

// ErrNilPeer is returned when a write names no destination.
var ErrNilPeer = errors.New("utpnet: nil peer")

// Conn is a uTP stream presented as a net.Conn.
type Conn struct {
	sock   *Socket
	stream *utp.UtpStream
	remote net.Addr

	readDeadline  *deadline
	writeDeadline *deadline

	closeOnce sync.Once
}

var _ net.Conn = (*Conn)(nil)

func newConn(sock *Socket, stream *utp.UtpStream) *Conn {
	var remote net.Addr
	if cid := stream.Cid(); cid != nil {
		if addr, err := peerUDPAddr(cid.Peer); err == nil {
			// Held for as long as the uTP socket has the connection
			// (demuxConn.HoldPeer).
			remote = sock.nativeAddr(addr)
		}
	}
	return &Conn{
		sock:          sock,
		stream:        stream,
		remote:        remote,
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
	}
}

// Stream returns the underlying uTP stream, for callers that need the
// context-taking API or the connection id.
func (c *Conn) Stream() *utp.UtpStream { return c.stream }

// Read reads data from the connection, honoring the read deadline.
func (c *Conn) Read(b []byte) (int, error) {
	return c.withDeadline(c.readDeadline, func(ctx context.Context) (int, error) {
		return c.stream.Read(ctx, b)
	})
}

// Write writes all of b, or reports how much was written. If the write
// deadline moves while it waits, it continues with the remainder.
func (c *Conn) Write(b []byte) (int, error) {
	return c.writeContinuing(func(ctx context.Context, from int) (int, error) {
		return c.stream.Write(ctx, b[from:])
	})
}

// writeContinuing runs a write under the write deadline, continuing from
// what has been written if the deadline moves. op writes the bytes from
// offset from on and reports how many the stream accepted.
func (c *Conn) writeContinuing(op func(ctx context.Context, from int) (int, error)) (int, error) {
	written := 0
	_, err := c.withDeadline(c.writeDeadline, func(ctx context.Context) (int, error) {
		n, err := op(ctx, written)
		written += n
		if err != nil {
			// Nothing reported, so withDeadline retries the rest if the
			// deadline moved.
			return 0, err
		}
		return n, nil
	})
	return written, err
}

// withDeadline runs op under a context derived from d and translates the
// result into what a net.Conn caller expects.
//
// A deadline may move while op is blocked; a common pattern is one goroutine
// blocked in Read while another calls SetReadDeadline to unblock it. Moving
// the deadline cancels op's context, and the loop then retries against the
// new deadline or reports a timeout.
func (c *Conn) withDeadline(d *deadline, op func(context.Context) (int, error)) (int, error) {
	for {
		ctx, cancel, expired, changed := d.context(c.sock.ctx)
		if expired {
			cancel()
			return 0, timeoutError{}
		}
		n, err := op(ctx)
		cancel()

		if err != nil && errors.Is(err, context.Canceled) {
			select {
			case <-changed:
				// The deadline moved; the socket did not close. Retry against
				// the new deadline, which returns a timeout if it has
				// passed.
				if n == 0 {
					continue
				}
			default:
			}
		}
		return n, translateDeadlineError(err)
	}
}

// translateDeadlineError maps a context expiry to a net.Error whose Timeout
// method reports true, which is what net.Conn callers check for, and leaves
// other errors unchanged.
func translateDeadlineError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutError{}
	}
	return err
}

// Close closes the connection and returns at once; what has been written is
// still delivered in the background.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() { c.stream.Close() })
	return nil
}

// LocalAddr returns the local address.
func (c *Conn) LocalAddr() net.Addr { return c.sock.LocalAddr() }

// RemoteAddr returns the peer's address.
func (c *Conn) RemoteAddr() net.Addr { return c.remote }

// SetDeadline sets the read and write deadlines. Moving a deadline affects
// calls already waiting.
func (c *Conn) SetDeadline(t time.Time) error {
	c.readDeadline.set(t)
	c.writeDeadline.set(t)
	return nil
}

// SetReadDeadline sets the read deadline.
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.readDeadline.set(t)
	return nil
}

// SetWriteDeadline sets the write deadline.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.set(t)
	return nil
}

// deadline is a net-style deadline: it can be set at any time, from any
// goroutine, and affects calls already in progress. A fresh context is
// derived from it for each call.
type deadline struct {
	mu sync.Mutex
	at time.Time
	// changed is closed and replaced whenever the deadline moves, so a
	// blocked waiter can notice.
	changed chan struct{}
}

func newDeadline() *deadline {
	return &deadline{changed: make(chan struct{})}
}

func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	d.at = t
	close(d.changed)
	d.changed = make(chan struct{})
	d.mu.Unlock()
}

// context derives a context that expires at the deadline, and returns the
// channel that is closed if the deadline moves. It reports true if the
// deadline has already passed, in which case the caller should not attempt
// the operation.
func (d *deadline) context(parent context.Context) (context.Context, context.CancelFunc, bool, <-chan struct{}) {
	d.mu.Lock()
	at := d.at
	changed := d.changed
	d.mu.Unlock()

	if !at.IsZero() && !time.Now().Before(at) {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, true, changed
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if at.IsZero() {
		ctx, cancel = context.WithCancel(parent)
	} else {
		ctx, cancel = context.WithDeadline(parent, at)
	}
	go func() {
		select {
		case <-changed:
			// The deadline moved during the operation. Cancel, so the caller
			// re-evaluates against the new one.
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, false, changed
}

// snapshot returns the deadline and the channel that is closed when it
// moves.
func (d *deadline) snapshot() (time.Time, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.at, d.changed
}

// expired reports whether the deadline has passed, without starting the
// goroutine wait would.
func (d *deadline) expired() bool {
	d.mu.Lock()
	at := d.at
	d.mu.Unlock()
	return !at.IsZero() && !time.Now().Before(at)
}

// wait returns a channel that fires at the deadline, whether the deadline
// has already passed, and a stop function. A zero deadline gives a channel
// that fires only once the deadline is changed. Serving the channel takes a
// goroutine, which stop ends; call stop exactly once on every path.
func (d *deadline) wait() (<-chan time.Time, bool, func()) {
	d.mu.Lock()
	at := d.at
	changed := d.changed
	d.mu.Unlock()

	if !at.IsZero() && !time.Now().Before(at) {
		return nil, true, func() {}
	}

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	ch := make(chan time.Time, 1)

	if at.IsZero() {
		go func() {
			select {
			case <-changed:
				close(ch)
			case <-done:
			}
		}()
		return ch, false, stop
	}

	timer := time.NewTimer(time.Until(at))
	go func() {
		defer timer.Stop()
		select {
		case t := <-timer.C:
			ch <- t
		case <-changed:
			close(ch)
		case <-done:
		}
	}()
	return ch, false, stop
}

// CloseWrite shuts down the sending side and leaves the receiving side open,
// like net.TCPConn.CloseWrite. Callers can detect it with a type assertion
// to interface{ CloseWrite() error }.
func (c *Conn) CloseWrite() error {
	return c.stream.CloseWrite()
}

// WriteBuffers writes several buffers as one stream write without joining
// them first, saving a copy. net.Buffers.WriteTo does not use it, so call it
// directly. If the write deadline moves while it waits, it continues with
// the remainder, as Write does.
func (c *Conn) WriteBuffers(bufs net.Buffers) (int64, error) {
	n, err := c.writeContinuing(func(ctx context.Context, from int) (int, error) {
		return c.stream.WriteV(ctx, buffersFrom(bufs, from))
	})
	return int64(n), err
}

// buffersFrom returns bufs without their first n bytes, sharing storage.
func buffersFrom(bufs net.Buffers, n int) [][]byte {
	for i, b := range bufs {
		if n < len(b) {
			rest := append([][]byte{b[n:]}, bufs[i+1:]...)
			return rest
		}
		n -= len(b)
	}
	return nil
}

// CloseRead shuts down the receiving side and leaves the sending side open,
// like net.TCPConn.CloseRead. Data the peer sends afterwards is acknowledged,
// so the peer is not stalled and can finish its close, and then discarded.
func (c *Conn) CloseRead() error {
	return c.stream.CloseRead()
}
