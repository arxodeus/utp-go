package utp

import (
	"errors"
	"io"
	"net"
	"time"

	utpgo "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/utpnet"
)

var (
	// ErrConnClosed is returned by a Conn's Read and Write after its Close.
	ErrConnClosed    = errors.New("closed")
	errConnDestroyed = errors.New("destroyed")
)

// Conn is a uTP connection.
type Conn struct {
	s  *Socket
	nc *utpnet.Conn

	localAddr  net.Addr
	remoteAddr net.Addr
	// remoteKey is remoteAddr as the engine's write-error hook names it.
	remoteKey string

	// Guarded by mu.
	//
	// us is the engine's stream while the connection lives, and nil once
	// it has ended or its Socket has closed: go-libutp's field of that name,
	// which is nil once libutp has destroyed the socket.
	us          *utpgo.UtpStream
	closed      bool
	err         error
	userOnError func(error)
}

var _ net.Conn = (*Conn)(nil)

func (s *Socket) newConn(nc *utpnet.Conn) (*Conn, error) {
	c := &Conn{
		s:          s,
		nc:         nc,
		localAddr:  s.pc.LocalAddr(),
		remoteAddr: nc.RemoteAddr(),
		us:         nc.Stream(),
	}
	if c.remoteAddr != nil {
		c.remoteKey = c.remoteAddr.String()
	}
	mu.Lock()
	if s.closed {
		mu.Unlock()
		nc.Close()
		return nil, errSocketClosed
	}
	s.conns[c] = struct{}{}
	mu.Unlock()
	done := c.us.Done()
	go func() {
		<-done
		mu.Lock()
		delete(s.conns, c)
		c.us = nil
		mu.Unlock()
	}()
	return c, nil
}

// fail ends the connection with err, which its Read and Write then return.
func (c *Conn) fail(err error) {
	mu.Lock()
	if c.err == nil {
		c.err = err
	}
	mu.Unlock()
	c.nc.Close()
}

// translate turns what the engine returned into what go-libutp returns:
// a failure's own error, ErrConnClosed after Close, and otherwise the
// engine's -- io.EOF at the end of the stream, a net.Error on a deadline.
func (c *Conn) translate(err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	switch {
	case c.err != nil:
		return c.err
	case c.closed:
		return ErrConnClosed
	case c.us == nil:
		return errConnDestroyed
	}
	return err
}

func (c *Conn) Read(b []byte) (int, error) {
	n, err := c.nc.Read(b)
	return n, c.translate(err)
}

func (c *Conn) Write(b []byte) (int, error) {
	mu.Lock()
	err := c.err
	closed := c.closed
	mu.Unlock()
	switch {
	case err != nil:
		return 0, err
	case closed:
		return 0, ErrConnClosed
	}
	n, err := c.nc.Write(b)
	return n, c.translate(err)
}

// Close closes the connection. What has been written is still delivered;
// Read and Write return ErrConnClosed from now on.
func (c *Conn) Close() error {
	mu.Lock()
	c.closed = true
	mu.Unlock()
	return c.nc.Close()
}

func (c *Conn) LocalAddr() net.Addr  { return c.localAddr }
func (c *Conn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *Conn) SetDeadline(t time.Time) error      { return c.nc.SetDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.nc.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.nc.SetWriteDeadline(t) }

// WriteBufferLen is this connection's SendBuffer: libutp's opt_sndbuf, the
// ceiling on its congestion window (utp_getsockopt).
func (c *Conn) WriteBufferLen() int {
	return int(c.nc.Stream().WindowSize())
}

// SetWriteBufferLen sets this connection's SendBuffer, with effect at once
// (utp_setsockopt). It panics on a value libutp refuses, as go-libutp does.
func (c *Conn) SetWriteBufferLen(len int) {
	if len < 1 {
		panic(-1)
	}
	c.nc.Stream().SetWindowSize(uint32(len))
}

// OnError sets a handler for errors that do not end the connection, such as
// a datagram the operating system would not send. It is called on a
// goroutine of its own.
func (c *Conn) OnError(f func(error)) {
	mu.Lock()
	c.userOnError = f
	mu.Unlock()
}
