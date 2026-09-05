// driver.h -- libutp with no socket and no clock of its own.
//
// The socket-backed peer in bridge.h is what the interoperability tests use:
// real UDP, real time, real concurrency. That is the right shape for proving
// a transfer completes, and the wrong shape for comparing two implementations
// packet by packet, because nothing about it is reproducible.
//
// This driver is the other shape. libutp reads the clock and the random
// number source only through callbacks, so both can be supplied: the driver
// has a virtual clock that only moves when told, and a scripted random
// source. Packets go in through inject and come out through a capture list.
// Nothing runs on its own, so the same script produces the same bytes every
// time.

#ifndef LIBUTP_DRIVER_H
#define LIBUTP_DRIVER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct libutp_driver libutp_driver;

// Create a driver. now_micros seeds the virtual clock.
libutp_driver *libutp_driver_create(uint64_t now_micros);
void libutp_driver_destroy(libutp_driver *d);

// Queue the values utp_call_get_random will return, in order. Once the queue
// is exhausted the last value repeats. libutp draws its connection id and
// initial sequence number from here, so this is what pins them.
void libutp_driver_push_random(libutp_driver *d, uint32_t value);

// The virtual clock. Nothing advances it but these.
void libutp_driver_set_time(libutp_driver *d, uint64_t now_micros);
void libutp_driver_advance(libutp_driver *d, uint64_t micros);
uint64_t libutp_driver_now(libutp_driver *d);

// Start an outgoing connection; emits a SYN.
int libutp_driver_connect(libutp_driver *d);
// Accept the next incoming SYN.
void libutp_driver_listen(libutp_driver *d);

// Feed one packet in, as if it had arrived from the peer.
int libutp_driver_inject(libutp_driver *d, const void *buf, size_t len);

// Periodic work. Neither happens on its own.
void libutp_driver_check_timeouts(libutp_driver *d);
void libutp_driver_issue_acks(libutp_driver *d);

// Packets libutp emitted, oldest first.
int libutp_driver_emitted_count(libutp_driver *d);
// Copy emitted packet i; returns its length, or -1 if i is out of range.
long libutp_driver_emitted_get(libutp_driver *d, int i, void *buf, size_t len);
void libutp_driver_emitted_clear(libutp_driver *d);

long libutp_driver_write(libutp_driver *d, const void *buf, size_t len);
long libutp_driver_read(libutp_driver *d, void *buf, size_t len);
int libutp_driver_state(libutp_driver *d);
int libutp_driver_error(libutp_driver *d);
void libutp_driver_close(libutp_driver *d);

#ifdef __cplusplus
}
#endif

#endif // LIBUTP_DRIVER_H
