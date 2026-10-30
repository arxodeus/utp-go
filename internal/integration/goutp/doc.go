// Package goutp tests this library against go-utp's pure Go engine
// (github.com/anacrolix/go-utp/purego), a cgo-free port of libutp, as an
// interop partner alongside libutp itself (internal/integration/libutp).
//
// It is a module of its own so that go-utp and its dependencies stay out of
// the library's module graph. Run it from here:
//
//	cd internal/integration/goutp && go test ./...
//
// purego follows libutp closely but departs from it in places: it resends a
// SYN-ACK, flushes as acknowledgements free the window, reports a reset
// during the handshake as refused, and reads a selective ack one bit short of
// where libutp reads it.
package goutp
