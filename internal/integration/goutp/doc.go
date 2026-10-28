// Package goutp tests this library against go-utp's pure Go engine
// (github.com/anacrolix/go-utp/purego), a port of libutp to Go that needs no
// cgo, as a third interop partner after libutp itself (native/libutp) and
// this library.
//
// It is a module of its own so that go-utp and what it requires stay out of
// the library's module graph. Run it from here:
//
//	cd integration/goutp && go test ./...
//
// purego follows libutp closely but departs from it in places, and so finds
// what libutp cannot: its package documentation lists a resent SYN-ACK, a
// sender that flushes as acknowledgements free the window, a reset during
// the handshake reported as refused, and a selective acknowledgement read
// one bit short of where libutp reads it.
package goutp
