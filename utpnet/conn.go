package utpnet

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	utp "github.com/zen-eth/utp-go"
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
			remote = addr
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

func (c *Conn) Read(b []byte) (int, error) {
	return c.withDeadline(c.readDeadline, func(ctx context.Context) (int, error) {
		return c.stream.Read(ctx, b)
	})
}

func (c *Conn) Write(b []byte) (int, error) {
	return c.withDeadline(c.writeDeadline, func(ctx context.Context) (int, error) {
		return c.stream.Write(ctx, b)
	})
}

// withDeadline runs op under a context derived from d, and translates what
// comes back into what a net.Conn caller expects.
//
// The loop is there because a deadline can move while op is blocked, which
// the standard library supports and callers use -- one goroutine blocked in
// Read, another calling SetReadDeadline to unblock it. Moving the deadline
// cancels op's context; this then re-reads the deadline and either retries
// against the new one or reports the timeout. Without the loop, moving a
// deadline surfaced as "context canceled", which no net.Conn caller checks
// for and every one of them would treat as fatal.
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
				// The deadline moved rather than the socket closing. Try
				// again against the deadline as it now stands; if that has
				// already passed, the next pass returns a timeout.
				if n == 0 {
					continue
				}
			default:
			}
		}
		return n, translateDeadlineError(err)
	}
}

// translateDeadlineError maps a context expiry onto the net.Error a caller
// expects, and leaves everything else alone.
//
// Without this, a Read that timed out would return "context deadline
// exceeded", which no net.Conn caller checks for -- they check
// err.(net.Error).Timeout() -- and would be treated as a fatal error that
// tears the connection down.
func translateDeadlineError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutError{}
	}
	return err
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() { c.stream.Close() })
	return nil
}

func (c *Conn) LocalAddr() net.Addr { return c.sock.LocalAddr() }

func (c *Conn) RemoteAddr() net.Addr { return c.remote }

func (c *Conn) SetDeadline(t time.Time) error {
	c.readDeadline.set(t)
	c.writeDeadline.set(t)
	return nil
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.readDeadline.set(t)
	return nil
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.set(t)
	return nil
}

// deadline is a net-style deadline: settable at any time, from any goroutine,
// affecting calls already in progress.
//
// The standard library's deadlines have that property and callers rely on it
// -- a common pattern is one goroutine blocked in Read and another calling
// SetReadDeadline(time.Now()) to unblock it. A context captured when Read
// started cannot do that, so the deadline is kept here and a fresh context is
// derived per call.
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
// channel that closes if the deadline is moved.
//
// It reports true if the deadline has already passed, in which case the
// caller should not attempt the operation at all.
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
			// The deadline moved while the operation was in flight. Cancel so
			// the caller re-evaluates against the new one.
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, false, changed
}

// expired reports whether the deadline has already passed.
//
// It exists for callers that want only that answer. wait starts a goroutine to
// serve its channel; a caller that discards the channel would leak it, which
// is what WriteTo used to do -- once per datagram, for the life of the
// process. See TestSharedPortDoesNotLeakGoroutines.
func (d *deadline) expired() bool {
	d.mu.Lock()
	at := d.at
	d.mu.Unlock()
	return !at.IsZero() && !time.Now().Before(at)
}

// wait returns a channel that fires at the deadline, reports whether the
// deadline has already passed, and returns a stop function the caller must
// call when it is done with the channel. A zero deadline gives a channel that
// never fires until the deadline is changed.
//
// The stop function is the whole point of the signature. Serving the channel
// takes a goroutine, and until this returned one there was no way to end that
// goroutine early: it parked until the deadline was next *changed*, which for
// a caller that never sets one is never. Every ReadFrom left one behind.
//
// Call stop exactly once, on every path out.
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

// CloseWrite finishes the sending side and leaves the reading side open, as
// net.TCPConn.CloseWrite does.
//
// It is here because callers type-assert for it: anything holding a net.Conn
// that wants a half-close looks for `interface{ CloseWrite() error }` rather
// than for a concrete type. A BitTorrent client uses it to say it has finished
// uploading while it is still downloading.
func (c *Conn) CloseWrite() error {
	return c.stream.CloseWrite()
}

// WriteBuffers writes several buffers as one stream write, without joining
// them first. It mirrors net.Buffers' shape, and libutp's utp_writev.
//
// The saving is one copy. Writing a header and a payload separately costs two
// Write calls (and two packets' worth of framing decisions); joining them
// first costs a copy into the joined slice and another inside Write. This
// costs one.
//
// net.Buffers.WriteTo cannot reach this on its own -- the standard library
// dispatches to an unexported interface only *net.TCPConn satisfies -- so a
// caller that wants the saving calls it directly.
func (c *Conn) WriteBuffers(bufs net.Buffers) (int64, error) {
	n, err := c.withDeadline(c.writeDeadline, func(ctx context.Context) (int, error) {
		return c.stream.WriteV(ctx, bufs)
	})
	return int64(n), err
}

// CloseRead finishes the receiving side and leaves the sending side open, as
// net.TCPConn.CloseRead does. What the peer sends afterwards is still
// acknowledged -- so the peer is never stalled and can finish its own close --
// and then discarded.
//
// Type-asserted for in the same way as CloseWrite:
// `interface{ CloseRead() error }`.
func (c *Conn) CloseRead() error {
	return c.stream.CloseRead()
}
