package goutp

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/go-utp/purego"
	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

// quietLogger is ours, quiet below critical.
func quietLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// puregoLogger is theirs, quiet below error.
func puregoLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func makePayload(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// pair is one of our sockets and one of purego's that can reach each other.
type pair struct {
	ours   *utp.UtpSocket
	theirs *purego.Socket
	// theirPeer is purego's socket as ours addresses it; ourAddr is ours as
	// purego dials it.
	theirPeer utp.ConnectionPeer
	ourAddr   string
	network   string
}

// udpPair is a pair over loopback UDP.
func udpPair(t *testing.T, ctx context.Context) *pair {
	t.Helper()
	ours, err := utp.Bind(ctx, "udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ours.Close)
	theirs, err := purego.NewSocket("udp4", "127.0.0.1:0", purego.WithLogger(puregoLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = theirs.Close() })
	return &pair{
		ours: ours, theirs: theirs,
		theirPeer: utp.NewUdpPeer(theirs.LocalAddr().(*net.UDPAddr)),
		ourAddr:   ours.LocalAddr().String(),
		network:   "udp4",
	}
}

// connect opens a connection between the two, with ours dialling or
// accepting.
func (p *pair) connect(t *testing.T, ctx context.Context, oursDials bool) (*utp.UtpStream, net.Conn) {
	t.Helper()
	type accepted struct {
		ours   *utp.UtpStream
		theirs net.Conn
		err    error
	}
	done := make(chan accepted, 1)
	var ours *utp.UtpStream
	var theirs net.Conn
	var err error
	if oursDials {
		go func() {
			c, err := p.theirs.Accept()
			done <- accepted{theirs: c, err: err}
		}()
		ours, err = p.ours.Connect(ctx, p.theirPeer, utp.NewConnectionConfig())
		if err != nil {
			t.Fatalf("our connect to purego: %v", err)
		}
		a := <-done
		if a.err != nil {
			t.Fatalf("purego accept: %v", a.err)
		}
		theirs = a.theirs
	} else {
		go func() {
			s, err := p.ours.Accept(ctx, utp.NewConnectionConfig())
			done <- accepted{ours: s, err: err}
		}()
		theirs, err = p.theirs.DialContext(ctx, p.network, p.ourAddr)
		if err != nil {
			t.Fatalf("purego dial to ours: %v", err)
		}
		a := <-done
		if a.err != nil {
			t.Fatalf("our accept: %v", a.err)
		}
		ours = a.ours
	}
	t.Cleanup(func() { ours.Close(); _ = theirs.Close() })
	return ours, theirs
}

// readAllOurs reads ours to the end of the stream.
func readAllOurs(ctx context.Context, s *utp.UtpStream, sizeHint int) ([]byte, error) {
	buf := make([]byte, 0, sizeHint)
	n, err := s.ReadToEOF(ctx, &buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return buf[:min(n, len(buf))], err
	}
	return buf[:min(n, len(buf))], nil
}

// readAllTheirs reads purego's to the end of the stream, or the deadline.
func readAllTheirs(c net.Conn, deadline time.Time) ([]byte, error) {
	_ = c.SetReadDeadline(deadline)
	return io.ReadAll(c)
}

func roleName(oursDials bool) string {
	if oursDials {
		return "ours dials"
	}
	return "purego dials"
}

// helloFirst makes the dialler speak first when purego accepted and is to
// send: libutp completes an incoming connection only on the first data packet
// (utp_internal.cpp:2158-2161) and writes nothing before that
// (utp_writev, :3181), and purego, its port, likewise holds every write until
// the dialler has sent something -- measured, its Write still blocked after
// 10 s. Protocols over uTP have the dialler speak first, as BitTorrent's
// handshake does. This library completes the connection on the handshake
// instead (DEVIATIONS.md, "Completing an incoming connection"), so when ours
// accepts it may send first, and the tests let it.
func helloFirst(ctx context.Context, ours *utp.UtpStream, theirs net.Conn, oursDials, oursSends bool) error {
	if !oursDials || oursSends {
		return nil
	}
	if _, err := ours.Write(ctx, []byte{0x68}); err != nil {
		return fmt.Errorf("our hello: %w", err)
	}
	deadline, _ := ctx.Deadline()
	_ = theirs.SetReadDeadline(deadline)
	hello := make([]byte, 1)
	if _, err := io.ReadFull(theirs, hello); err != nil {
		return fmt.Errorf("purego reading our hello: %w", err)
	}
	return nil
}

// transfer sends payload one way on an open connection, the sender closing
// when done, and checks that the receiver read exactly it.
func transfer(ctx context.Context, ours *utp.UtpStream, theirs net.Conn, oursDials, oursSends bool, payload []byte) error {
	if err := helloFirst(ctx, ours, theirs, oursDials, oursSends); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	sent := make(chan error, 1)
	var got []byte
	var recvErr error
	if oursSends {
		go func() {
			_, err := ours.Write(ctx, payload)
			ours.Close()
			sent <- err
		}()
		got, recvErr = readAllTheirs(theirs, deadline)
	} else {
		go func() {
			_, err := theirs.Write(payload)
			if cerr := theirs.Close(); err == nil {
				err = cerr
			}
			sent <- err
		}()
		got, recvErr = readAllOurs(ctx, ours, len(payload))
	}
	if recvErr != nil {
		return fmt.Errorf("receiver: %w after %d of %d bytes", recvErr, len(got), len(payload))
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("received %d bytes of %d, and they differ", len(got), len(payload))
	}
	select {
	case err := <-sent:
		if err != nil {
			return fmt.Errorf("sender: %w", err)
		}
	case <-ctx.Done():
		return fmt.Errorf("sender did not finish: %w", ctx.Err())
	}
	return nil
}

func sender(oursSends bool) string {
	if oursSends {
		return "ours sends"
	}
	return "purego sends"
}

// A transfer completes, intact, in every combination of who dials and who
// sends.
func TestPuregoTransfer(t *testing.T) {
	for _, oursDials := range []bool{true, false} {
		for _, oursSends := range []bool{true, false} {
			t.Run(roleName(oursDials)+", "+sender(oursSends), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				ours, theirs := udpPair(t, ctx).connect(t, ctx, oursDials)
				payload := makePayload(t, 4<<20)
				start := time.Now()
				if err := transfer(ctx, ours, theirs, oursDials, oursSends, payload); err != nil {
					t.Fatal(err)
				}
				el := time.Since(start)
				t.Logf("%d bytes in %v (%.1f Mbps)", len(payload), el.Round(time.Millisecond),
					float64(len(payload))*8/el.Seconds()/1e6)
			})
		}
	}
}

// Both ends send at once on one connection, each half-closing when done,
// and each reads everything the other sent.
func TestPuregoBothDirectionsAtOnce(t *testing.T) {
	for _, oursDials := range []bool{true, false} {
		t.Run(roleName(oursDials), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			p := udpPair(t, ctx)
			ours, theirs := p.connect(t, ctx, oursDials)
			toTheirs, toOurs := makePayload(t, 2<<20), makePayload(t, 2<<20)
			deadline, _ := ctx.Deadline()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				if _, err := ours.Write(ctx, toTheirs); err != nil {
					t.Errorf("our write: %v", err)
				}
				if err := ours.CloseWrite(); err != nil {
					t.Errorf("our CloseWrite: %v", err)
				}
			}()
			go func() {
				defer wg.Done()
				if _, err := theirs.Write(toOurs); err != nil {
					t.Errorf("purego write: %v", err)
				}
				if err := theirs.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
					t.Errorf("purego CloseWrite: %v", err)
				}
			}()
			var theirsGot []byte
			var theirsErr error
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				theirsGot, theirsErr = readAllTheirs(theirs, deadline)
			}()
			oursGot, oursErr := readAllOurs(ctx, ours, len(toOurs))
			<-readDone
			wg.Wait()
			if oursErr != nil || !bytes.Equal(oursGot, toOurs) {
				t.Errorf("ours received %d of %d bytes from purego (err %v), equal: %v",
					len(oursGot), len(toOurs), oursErr, bytes.Equal(oursGot, toOurs))
			}
			if theirsErr != nil || !bytes.Equal(theirsGot, toTheirs) {
				t.Errorf("purego received %d of %d bytes from ours (err %v), equal: %v",
					len(theirsGot), len(toTheirs), theirsErr, bytes.Equal(theirsGot, toTheirs))
			}
		})
	}
}

// Many connections between the same two sockets at once, each carrying its
// own payload, routed and delivered without mixing them up.
func TestPuregoConcurrentConnections(t *testing.T) {
	const conns, size = 16, 256 << 10
	for _, oursDials := range []bool{true, false} {
		t.Run(roleName(oursDials), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			p := udpPair(t, ctx)
			var wg sync.WaitGroup
			errs := make(chan error, conns)
			for i := 0; i < conns; i++ {
				ours, theirs := p.connect(t, ctx, oursDials)
				oursSends := i%2 == 0
				payload := makePayload(t, size)
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := transfer(ctx, ours, theirs, oursDials, oursSends, payload); err != nil {
						errs <- fmt.Errorf("connection %d, %s: %w", i, sender(oursSends), err)
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}
}

// Writes far smaller than a packet, as an interactive protocol makes them,
// arrive in order and whole: both engines coalesce small writes (Nagle), and
// the two must agree on where one packet's bytes end and the next's begin.
func TestPuregoSmallWrites(t *testing.T) {
	const writes, size = 2000, 37
	for _, oursSends := range []bool{true, false} {
		t.Run(sender(oursSends), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ours, theirs := udpPair(t, ctx).connect(t, ctx, true)
			if err := helloFirst(ctx, ours, theirs, true, oursSends); err != nil {
				t.Fatal(err)
			}
			payload := makePayload(t, writes*size)
			deadline, _ := ctx.Deadline()
			sent := make(chan error, 1)
			var got []byte
			var err error
			if oursSends {
				go func() {
					for i := 0; i < writes; i++ {
						if _, err := ours.Write(ctx, payload[i*size:(i+1)*size]); err != nil {
							sent <- fmt.Errorf("our write %d: %w", i, err)
							return
						}
					}
					ours.Close()
					sent <- nil
				}()
				got, err = readAllTheirs(theirs, deadline)
			} else {
				go func() {
					for i := 0; i < writes; i++ {
						if _, err := theirs.Write(payload[i*size : (i+1)*size]); err != nil {
							sent <- fmt.Errorf("purego write %d: %w", i, err)
							return
						}
					}
					sent <- theirs.Close()
				}()
				got, err = readAllOurs(ctx, ours, len(payload))
			}
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("%d of %d bytes, err %v, equal %v", len(got), len(payload), err, bytes.Equal(got, payload))
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Request and response over a half-closed connection: the side that asks
// dials, sends its request and closes its writing half; the other reads to
// the end, answers and closes. The answer arrives on the half still open.
func TestPuregoRequestResponse(t *testing.T) {
	for _, oursAsks := range []bool{true, false} {
		t.Run(map[bool]string{true: "ours asks", false: "purego asks"}[oursAsks], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ours, theirs := udpPair(t, ctx).connect(t, ctx, oursAsks)
			request, response := makePayload(t, 300<<10), makePayload(t, 700<<10)
			deadline, _ := ctx.Deadline()
			answered := make(chan error, 1)
			var got []byte
			var err error
			if oursAsks {
				go func() {
					got, err := readAllTheirs(theirs, deadline)
					if err != nil || !bytes.Equal(got, request) {
						answered <- fmt.Errorf("purego read the request: %d of %d bytes, err %v", len(got), len(request), err)
						return
					}
					_, err = theirs.Write(response)
					if cerr := theirs.Close(); err == nil {
						err = cerr
					}
					answered <- err
				}()
				if _, err := ours.Write(ctx, request); err != nil {
					t.Fatalf("our write: %v", err)
				}
				if err := ours.CloseWrite(); err != nil {
					t.Fatalf("our CloseWrite: %v", err)
				}
				got, err = readAllOurs(ctx, ours, len(response))
			} else {
				go func() {
					got, err := readAllOurs(ctx, ours, len(request))
					if err != nil || !bytes.Equal(got, request) {
						answered <- fmt.Errorf("our read of the request: %d of %d bytes, err %v", len(got), len(request), err)
						return
					}
					_, err = ours.Write(ctx, response)
					ours.Close()
					answered <- err
				}()
				if _, err := theirs.Write(request); err != nil {
					t.Fatalf("purego write: %v", err)
				}
				if err := theirs.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
					t.Fatalf("purego CloseWrite: %v", err)
				}
				got, err = readAllTheirs(theirs, deadline)
			}
			if aerr := <-answered; aerr != nil {
				t.Fatalf("answering side: %v", aerr)
			}
			if err != nil || !bytes.Equal(got, response) {
				t.Fatalf("read of the response: %d of %d bytes, err %v", len(got), len(response), err)
			}
		})
	}
}
