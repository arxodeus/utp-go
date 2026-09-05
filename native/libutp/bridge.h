// bridge.h -- a small C API over libutp, for interoperability testing.
//
// libutp is an embedding library: it does no I/O of its own and is not
// thread-safe. This bridge gives it a UDP socket and an event loop, serialises
// every call into that loop, and exposes a handful of blocking-free entry
// points that Go can drive.
//
// One peer owns one connection. That is all an interop test needs, and it
// keeps the lifetime rules -- particularly that a utp_socket must not be
// touched after UTP_STATE_DESTROYING -- simple enough to get right.

#ifndef LIBUTP_BRIDGE_H
#define LIBUTP_BRIDGE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#include "bridge_state.h"

typedef struct libutp_peer libutp_peer;

// Create a peer bound to 127.0.0.1 on the given port. Pass 0 to let the
// kernel choose; libutp_peer_port then reports what was chosen.
// Returns NULL on failure.
libutp_peer *libutp_peer_create(uint16_t port);

// Destroy a peer. The event loop must have returned first.
void libutp_peer_destroy(libutp_peer *p);

// Run the event loop until libutp_peer_stop. Blocks; call it on its own
// goroutine.
void libutp_peer_run(libutp_peer *p);
void libutp_peer_stop(libutp_peer *p);

uint16_t libutp_peer_port(libutp_peer *p);

// Start an outgoing connection to 127.0.0.1:remote_port. Returns 0 if the
// attempt was started, -1 if a socket already exists.
int libutp_peer_connect(libutp_peer *p, uint16_t remote_port);

// Accept the next incoming connection. Until this is called, incoming SYNs
// are refused by the firewall callback.
void libutp_peer_listen(libutp_peer *p);

int libutp_peer_state(libutp_peer *p);
int libutp_peer_error(libutp_peer *p);

// Queue data for sending. Returns the number of bytes queued, which is always
// len: the queue grows as needed and libutp drains it as the window allows.
long libutp_peer_queue_write(libutp_peer *p, const void *buf, size_t len);

// Bytes still queued and not yet handed to libutp.
long libutp_peer_pending_write(libutp_peer *p);

// Copy out received data. Returns the number of bytes copied, 0 if none is
// buffered yet.
long libutp_peer_read(libutp_peer *p, void *buf, size_t len);

// Bytes received in total, whether or not they have been read out.
uint64_t libutp_peer_bytes_received(libutp_peer *p);

// Close once everything queued has been handed to libutp.
void libutp_peer_close(libutp_peer *p);

#ifdef __cplusplus
}
#endif

#endif // LIBUTP_BRIDGE_H
