// driver.cpp -- see driver.h.
//
// Single-threaded by construction: every entry point is called from Go and
// runs to completion before returning, so there is no lock here. That is the
// point -- the socket-backed peer needs a mutex and a thread, and neither is
// reproducible.

#include "driver.h"

#include "bridge_state.h"
#include "utp.h"

#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

#define DRIVER_MAX_EMITTED 4096
#define DRIVER_MAX_RANDOM 64

struct emitted_packet {
	unsigned char *data;
	size_t len;
};

struct libutp_driver {
	utp_context *ctx;
	utp_socket *sock;

	uint64_t now_micros;

	uint32_t randoms[DRIVER_MAX_RANDOM];
	int random_count;
	int random_next;

	emitted_packet emitted[DRIVER_MAX_EMITTED];
	int emitted_count;

	int state;
	int err;
	int listening;
	int want_close;
	int close_sent;

	unsigned char *rx;
	size_t rx_len;
	size_t rx_cap;

	unsigned char *tx;
	size_t tx_len;
	size_t tx_cap;
};

// A fixed peer address. The driver never touches a socket, so the only thing
// that matters is that it is stable and consistent between calls.
static void driver_peer_addr(struct sockaddr_in *addr) {
	memset(addr, 0, sizeof(*addr));
	addr->sin_family = AF_INET;
	addr->sin_addr.s_addr = htonl(0x7f000001); // 127.0.0.1
	addr->sin_port = htons(23456);
}

static int drv_append(unsigned char **buf, size_t *len, size_t *cap,
                      const void *src, size_t n) {
	if (n == 0) return 0;
	if (*len + n > *cap) {
		size_t want = *cap ? *cap : 4096;
		while (want < *len + n) want *= 2;
		unsigned char *grown = (unsigned char *)realloc(*buf, want);
		if (!grown) return -1;
		*buf = grown;
		*cap = want;
	}
	memcpy(*buf + *len, src, n);
	*len += n;
	return 0;
}

static void drv_consume(unsigned char *buf, size_t *len, size_t n) {
	if (n >= *len) {
		*len = 0;
		return;
	}
	memmove(buf, buf + n, *len - n);
	*len -= n;
}

static void drv_pump_writes(libutp_driver *d) {
	while (d->sock && d->tx_len > 0) {
		ssize_t n = utp_write(d->sock, d->tx, d->tx_len);
		if (n <= 0) break;
		drv_consume(d->tx, &d->tx_len, (size_t)n);
	}
	if (d->want_close && !d->close_sent && d->tx_len == 0 && d->sock) {
		utp_close(d->sock);
		d->close_sent = 1;
	}
}

// --- callbacks --------------------------------------------------------------

static libutp_driver *drv_of(utp_callback_arguments *a) {
	return (libutp_driver *)utp_context_get_userdata(a->context);
}

static uint64 drv_sendto(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	if (d->emitted_count >= DRIVER_MAX_EMITTED) return 0;
	unsigned char *copy = (unsigned char *)malloc(a->len);
	if (!copy) return 0;
	memcpy(copy, a->buf, a->len);
	d->emitted[d->emitted_count].data = copy;
	d->emitted[d->emitted_count].len = a->len;
	d->emitted_count++;
	return 0;
}

static uint64 drv_on_read(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	drv_append(&d->rx, &d->rx_len, &d->rx_cap, a->buf, a->len);
	utp_read_drained(a->socket);
	return 0;
}

static uint64 drv_on_state_change(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	switch (a->state) {
	case UTP_STATE_CONNECT:
	case UTP_STATE_WRITABLE:
		if (d->state == LIBUTP_STATE_IDLE || d->state == LIBUTP_STATE_CONNECTING) {
			d->state = LIBUTP_STATE_CONNECTED;
		}
		drv_pump_writes(d);
		break;
	case UTP_STATE_EOF:
		d->state = LIBUTP_STATE_EOF;
		break;
	case UTP_STATE_DESTROYING:
		d->sock = NULL;
		d->state = LIBUTP_STATE_DESTROYED;
		break;
	}
	return 0;
}

static uint64 drv_on_error(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	d->err = a->error_code;
	d->state = LIBUTP_STATE_ERROR;
	return 0;
}

static uint64 drv_on_firewall(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	return (d->listening && d->sock == NULL) ? 0 : 1;
}

static uint64 drv_on_accept(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	if (d->sock == NULL) {
		d->sock = a->socket;
		d->state = LIBUTP_STATE_CONNECTED;
		drv_pump_writes(d);
	}
	return 0;
}

static uint64 drv_get_read_buffer_size(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	return (uint64)d->rx_len;
}

// libutp asks its embedder for the path MTU and uses the answer as the ceiling
// of its own MTU search (mtu_reset, utp_internal.cpp:1314-1322). With no
// callback registered utp_call_get_udp_mtu returns 0 (utp_callbacks.cpp:122),
// so the ceiling is 0, the floor stays 576, and mtu_search_update settles on
// (576 + 0) / 2 = 288-byte packets -- the underflow in its
// `mtu_ceiling - mtu_floor <= 16` test keeps the search from ever finishing.
// That is not libutp's behaviour, it is the behaviour of libutp wired up
// wrongly, and it made every throughput measurement taken against this driver
// an understatement.
//
// 1472 is a standard 1500-byte Ethernet MTU less 20 bytes of IPv4 header and
// 8 of UDP, which is what an embedder on an ordinary path reports.
static uint64 drv_get_udp_mtu(utp_callback_arguments *a) {
	(void)a;
	return 1472;
}

static uint64 drv_get_milliseconds(utp_callback_arguments *a) {
	return drv_of(a)->now_micros / 1000ULL;
}

static uint64 drv_get_microseconds(utp_callback_arguments *a) {
	return drv_of(a)->now_micros;
}

static uint64 drv_get_random(utp_callback_arguments *a) {
	libutp_driver *d = drv_of(a);
	if (d->random_count == 0) return 0;
	uint32_t v = d->randoms[d->random_next];
	// Hold at the last scripted value once the queue runs out, so a test that
	// scripts fewer draws than libutp makes still behaves predictably.
	if (d->random_next + 1 < d->random_count) d->random_next++;
	return (uint64)v;
}

static uint64 drv_log(utp_callback_arguments *a) {
	(void)a;
	return 0;
}

// --- lifecycle --------------------------------------------------------------

libutp_driver *libutp_driver_create(uint64_t now_micros) {
	libutp_driver *d = (libutp_driver *)calloc(1, sizeof(libutp_driver));
	if (!d) return NULL;
	d->now_micros = now_micros;
	d->state = LIBUTP_STATE_IDLE;

	d->ctx = utp_init(2);
	if (!d->ctx) {
		free(d);
		return NULL;
	}
	utp_context_set_userdata(d->ctx, d);

	utp_set_callback(d->ctx, UTP_SENDTO, &drv_sendto);
	utp_set_callback(d->ctx, UTP_ON_READ, &drv_on_read);
	utp_set_callback(d->ctx, UTP_ON_STATE_CHANGE, &drv_on_state_change);
	utp_set_callback(d->ctx, UTP_ON_ERROR, &drv_on_error);
	utp_set_callback(d->ctx, UTP_ON_FIREWALL, &drv_on_firewall);
	utp_set_callback(d->ctx, UTP_ON_ACCEPT, &drv_on_accept);
	utp_set_callback(d->ctx, UTP_GET_READ_BUFFER_SIZE, &drv_get_read_buffer_size);
	utp_set_callback(d->ctx, UTP_GET_UDP_MTU, &drv_get_udp_mtu);
	utp_set_callback(d->ctx, UTP_GET_MILLISECONDS, &drv_get_milliseconds);
	utp_set_callback(d->ctx, UTP_GET_MICROSECONDS, &drv_get_microseconds);
	utp_set_callback(d->ctx, UTP_GET_RANDOM, &drv_get_random);
	utp_set_callback(d->ctx, UTP_LOG, &drv_log);
	return d;
}

void libutp_driver_destroy(libutp_driver *d) {
	if (!d) return;
	if (d->ctx) utp_destroy(d->ctx);
	libutp_driver_emitted_clear(d);
	free(d->rx);
	free(d->tx);
	free(d);
}

void libutp_driver_push_random(libutp_driver *d, uint32_t value) {
	if (d->random_count >= DRIVER_MAX_RANDOM) return;
	d->randoms[d->random_count++] = value;
}

void libutp_driver_set_time(libutp_driver *d, uint64_t now_micros) {
	d->now_micros = now_micros;
}

void libutp_driver_advance(libutp_driver *d, uint64_t micros) {
	d->now_micros += micros;
}

uint64_t libutp_driver_now(libutp_driver *d) { return d->now_micros; }

int libutp_driver_connect(libutp_driver *d) {
	if (d->sock != NULL || d->ctx == NULL) return -1;
	d->sock = utp_create_socket(d->ctx);
	if (!d->sock) return -1;
	d->state = LIBUTP_STATE_CONNECTING;

	struct sockaddr_in to;
	driver_peer_addr(&to);
	utp_connect(d->sock, (struct sockaddr *)&to, sizeof(to));
	return 0;
}

void libutp_driver_listen(libutp_driver *d) { d->listening = 1; }

int libutp_driver_inject(libutp_driver *d, const void *buf, size_t len) {
	if (!d->ctx) return -1;
	struct sockaddr_in from;
	driver_peer_addr(&from);
	int rc = utp_process_udp(d->ctx, (const byte *)buf, len,
	                         (struct sockaddr *)&from, sizeof(from));
	drv_pump_writes(d);
	return rc;
}

void libutp_driver_check_timeouts(libutp_driver *d) {
	if (!d->ctx) return;
	utp_check_timeouts(d->ctx);
	drv_pump_writes(d);
}

void libutp_driver_issue_acks(libutp_driver *d) {
	if (!d->ctx) return;
	utp_issue_deferred_acks(d->ctx);
}

int libutp_driver_emitted_count(libutp_driver *d) { return d->emitted_count; }

long libutp_driver_emitted_get(libutp_driver *d, int i, void *buf, size_t len) {
	if (i < 0 || i >= d->emitted_count) return -1;
	size_t n = d->emitted[i].len < len ? d->emitted[i].len : len;
	memcpy(buf, d->emitted[i].data, n);
	return (long)d->emitted[i].len;
}

void libutp_driver_emitted_clear(libutp_driver *d) {
	for (int i = 0; i < d->emitted_count; i++) {
		free(d->emitted[i].data);
		d->emitted[i].data = NULL;
	}
	d->emitted_count = 0;
}

long libutp_driver_write(libutp_driver *d, const void *buf, size_t len) {
	if (drv_append(&d->tx, &d->tx_len, &d->tx_cap, buf, len) < 0) return -1;
	drv_pump_writes(d);
	return (long)len;
}

long libutp_driver_read(libutp_driver *d, void *buf, size_t len) {
	size_t n = d->rx_len < len ? d->rx_len : len;
	if (n > 0) {
		memcpy(buf, d->rx, n);
		drv_consume(d->rx, &d->rx_len, n);
	}
	return (long)n;
}

int libutp_driver_state(libutp_driver *d) { return d->state; }
int libutp_driver_error(libutp_driver *d) { return d->err; }

void libutp_driver_close(libutp_driver *d) {
	d->want_close = 1;
	drv_pump_writes(d);
}
