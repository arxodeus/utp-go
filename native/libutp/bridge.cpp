// bridge.cpp -- see bridge.h.
//
// Every libutp entry point is called with peer->mu held, from either the event
// loop or a Go-facing function. libutp is not thread-safe, so that lock is the
// whole concurrency story.

#include "bridge.h"

#include "utp.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

// --- a growable byte buffer -------------------------------------------------
// Plain malloc rather than std::vector: libutp is built -fno-exceptions, and
// there is no reason to drag the C++ allocator in for two byte queues.

struct byte_buf {
	unsigned char *data;
	size_t len;
	size_t cap;
};

static void buf_init(byte_buf *b) {
	b->data = NULL;
	b->len = 0;
	b->cap = 0;
}

static void buf_free(byte_buf *b) {
	free(b->data);
	buf_init(b);
}

static int buf_append(byte_buf *b, const void *src, size_t n) {
	if (n == 0) return 0;
	if (b->len + n > b->cap) {
		size_t cap = b->cap ? b->cap : 4096;
		while (cap < b->len + n) cap *= 2;
		unsigned char *grown = (unsigned char *)realloc(b->data, cap);
		if (!grown) return -1;
		b->data = grown;
		b->cap = cap;
	}
	memcpy(b->data + b->len, src, n);
	b->len += n;
	return 0;
}

static void buf_consume(byte_buf *b, size_t n) {
	if (n >= b->len) {
		b->len = 0;
		return;
	}
	memmove(b->data, b->data + n, b->len - n);
	b->len -= n;
}

// --- peer -------------------------------------------------------------------

struct libutp_peer {
	utp_context *ctx;
	utp_socket *sock;

	int fd;               // UDP socket
	int wake_r, wake_w;   // self-pipe, to break select() promptly
	uint16_t port;

	pthread_mutex_t mu;

	int state;
	int err;
	int listening;
	int stop;
	int want_close;
	int close_sent;

	uint64_t bytes_received;
	uint64_t last_timeout_check_ms;

	byte_buf rx;
	byte_buf tx;
};

static uint64_t now_us(void) {
	struct timeval tv;
	gettimeofday(&tv, NULL);
	return (uint64_t)tv.tv_sec * 1000000ULL + (uint64_t)tv.tv_usec;
}

static uint64_t now_ms(void) { return now_us() / 1000ULL; }

static void wake(libutp_peer *p) {
	char b = 1;
	ssize_t rc = write(p->wake_w, &b, 1);
	(void)rc;
}

// pump_writes hands as much queued data to libutp as its window allows.
// Must be called with mu held.
static void pump_writes(libutp_peer *p) {
	while (p->sock && p->tx.len > 0) {
		ssize_t n = utp_write(p->sock, p->tx.data, p->tx.len);
		if (n <= 0) break;   // window full; UTP_STATE_WRITABLE will call back
		buf_consume(&p->tx, (size_t)n);
	}
	if (p->want_close && !p->close_sent && p->tx.len == 0 && p->sock) {
		// Everything queued is now libutp's problem; it will flush before it
		// destroys the socket.
		utp_close(p->sock);
		p->close_sent = 1;
	}
}

// --- libutp callbacks -------------------------------------------------------

static libutp_peer *peer_of(utp_callback_arguments *a) {
	return (libutp_peer *)utp_context_get_userdata(a->context);
}

static uint64 cb_sendto(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	sendto(p->fd, a->buf, a->len, 0, a->address, a->address_len);
	return 0;
}

static uint64 cb_on_read(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	buf_append(&p->rx, a->buf, a->len);
	p->bytes_received += a->len;
	// Tell libutp the data is out of its buffer, so its receive window reopens.
	utp_read_drained(a->socket);
	return 0;
}

static uint64 cb_on_state_change(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	switch (a->state) {
	case UTP_STATE_CONNECT:
	case UTP_STATE_WRITABLE:
		if (p->state == LIBUTP_STATE_IDLE || p->state == LIBUTP_STATE_CONNECTING) {
			p->state = LIBUTP_STATE_CONNECTED;
		}
		pump_writes(p);
		break;
	case UTP_STATE_EOF:
		p->state = LIBUTP_STATE_EOF;
		break;
	case UTP_STATE_DESTROYING:
		// The socket pointer is invalid from here on.
		p->sock = NULL;
		p->state = LIBUTP_STATE_DESTROYED;
		break;
	}
	return 0;
}

static uint64 cb_on_error(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	p->err = a->error_code;
	p->state = LIBUTP_STATE_ERROR;
	return 0;
}

static uint64 cb_on_firewall(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	// Non-zero refuses the connection.
	return (p->listening && p->sock == NULL) ? 0 : 1;
}

static uint64 cb_on_accept(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	if (p->sock == NULL) {
		p->sock = a->socket;
		p->state = LIBUTP_STATE_CONNECTED;
		pump_writes(p);
	}
	return 0;
}

static uint64 cb_get_read_buffer_size(utp_callback_arguments *a) {
	libutp_peer *p = peer_of(a);
	// How much libutp has handed us that the application has not read yet.
	// This is what sizes the advertised receive window.
	return (uint64)p->rx.len;
}

static uint64 cb_get_milliseconds(utp_callback_arguments *a) {
	(void)a;
	return now_ms();
}

static uint64 cb_get_microseconds(utp_callback_arguments *a) {
	(void)a;
	return now_us();
}

static uint64 cb_get_random(utp_callback_arguments *a) {
	(void)a;
	return (uint64)random();
}

static uint64 cb_log(utp_callback_arguments *a) {
	(void)a;
	return 0;
}

// --- lifecycle --------------------------------------------------------------

static int set_nonblocking(int fd) {
	int flags = fcntl(fd, F_GETFL, 0);
	if (flags < 0) return -1;
	return fcntl(fd, F_SETFL, flags | O_NONBLOCK);
}

libutp_peer *libutp_peer_create(uint16_t port) {
	libutp_peer *p = (libutp_peer *)calloc(1, sizeof(libutp_peer));
	if (!p) return NULL;

	buf_init(&p->rx);
	buf_init(&p->tx);
	p->fd = -1;
	p->wake_r = p->wake_w = -1;
	p->state = LIBUTP_STATE_IDLE;
	pthread_mutex_init(&p->mu, NULL);

	p->fd = socket(AF_INET, SOCK_DGRAM, 0);
	if (p->fd < 0) goto fail;

	{
		struct sockaddr_in addr;
		memset(&addr, 0, sizeof(addr));
		addr.sin_family = AF_INET;
		addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
		addr.sin_port = htons(port);
		if (bind(p->fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) goto fail;

		socklen_t alen = sizeof(addr);
		if (getsockname(p->fd, (struct sockaddr *)&addr, &alen) < 0) goto fail;
		p->port = ntohs(addr.sin_port);
	}
	if (set_nonblocking(p->fd) < 0) goto fail;

	{
		int pipefd[2];
		if (pipe(pipefd) < 0) goto fail;
		p->wake_r = pipefd[0];
		p->wake_w = pipefd[1];
		set_nonblocking(p->wake_r);
		set_nonblocking(p->wake_w);
	}

	p->ctx = utp_init(2);
	if (!p->ctx) goto fail;
	utp_context_set_userdata(p->ctx, p);

	utp_set_callback(p->ctx, UTP_SENDTO, &cb_sendto);
	utp_set_callback(p->ctx, UTP_ON_READ, &cb_on_read);
	utp_set_callback(p->ctx, UTP_ON_STATE_CHANGE, &cb_on_state_change);
	utp_set_callback(p->ctx, UTP_ON_ERROR, &cb_on_error);
	utp_set_callback(p->ctx, UTP_ON_FIREWALL, &cb_on_firewall);
	utp_set_callback(p->ctx, UTP_ON_ACCEPT, &cb_on_accept);
	utp_set_callback(p->ctx, UTP_GET_READ_BUFFER_SIZE, &cb_get_read_buffer_size);
	// No defaults exist for these: libutp returns 0 when they are unset, which
	// would leave every timestamp and connection id zero.
	utp_set_callback(p->ctx, UTP_GET_MILLISECONDS, &cb_get_milliseconds);
	utp_set_callback(p->ctx, UTP_GET_MICROSECONDS, &cb_get_microseconds);
	utp_set_callback(p->ctx, UTP_GET_RANDOM, &cb_get_random);
	utp_set_callback(p->ctx, UTP_LOG, &cb_log);

	p->last_timeout_check_ms = now_ms();
	return p;

fail:
	if (p->ctx) utp_destroy(p->ctx);
	if (p->fd >= 0) close(p->fd);
	if (p->wake_r >= 0) close(p->wake_r);
	if (p->wake_w >= 0) close(p->wake_w);
	pthread_mutex_destroy(&p->mu);
	free(p);
	return NULL;
}

void libutp_peer_destroy(libutp_peer *p) {
	if (!p) return;
	pthread_mutex_lock(&p->mu);
	if (p->ctx) {
		utp_destroy(p->ctx);
		p->ctx = NULL;
		p->sock = NULL;
	}
	buf_free(&p->rx);
	buf_free(&p->tx);
	pthread_mutex_unlock(&p->mu);

	if (p->fd >= 0) close(p->fd);
	if (p->wake_r >= 0) close(p->wake_r);
	if (p->wake_w >= 0) close(p->wake_w);
	pthread_mutex_destroy(&p->mu);
	free(p);
}

uint16_t libutp_peer_port(libutp_peer *p) { return p->port; }

void libutp_peer_stop(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	p->stop = 1;
	pthread_mutex_unlock(&p->mu);
	wake(p);
}

void libutp_peer_run(libutp_peer *p) {
	unsigned char pkt[65536];

	for (;;) {
		pthread_mutex_lock(&p->mu);
		int stop = p->stop;
		pthread_mutex_unlock(&p->mu);
		if (stop) return;

		fd_set rf;
		FD_ZERO(&rf);
		FD_SET(p->fd, &rf);
		FD_SET(p->wake_r, &rf);
		int maxfd = p->fd > p->wake_r ? p->fd : p->wake_r;

		struct timeval tv;
		tv.tv_sec = 0;
		tv.tv_usec = 20000; // 20ms, so timeouts are checked promptly
		int rv = select(maxfd + 1, &rf, NULL, NULL, &tv);

		pthread_mutex_lock(&p->mu);

		if (rv > 0 && FD_ISSET(p->wake_r, &rf)) {
			char drain[256];
			while (read(p->wake_r, drain, sizeof(drain)) > 0) {
			}
		}

		if (rv > 0 && FD_ISSET(p->fd, &rf) && p->ctx) {
			for (;;) {
				struct sockaddr_in src;
				socklen_t slen = sizeof(src);
				ssize_t n = recvfrom(p->fd, pkt, sizeof(pkt), 0,
				                     (struct sockaddr *)&src, &slen);
				if (n < 0) break;
				utp_process_udp(p->ctx, pkt, (size_t)n,
				                (struct sockaddr *)&src, slen);
			}
			// libutp batches acks; without this they are only sent on the next
			// timeout tick, which throttles the sender badly.
			utp_issue_deferred_acks(p->ctx);
		}

		if (p->ctx) {
			uint64_t nowms = now_ms();
			if (nowms - p->last_timeout_check_ms >= 50) {
				utp_check_timeouts(p->ctx);
				p->last_timeout_check_ms = nowms;
			}
			pump_writes(p);
		}

		pthread_mutex_unlock(&p->mu);
	}
}

int libutp_peer_connect(libutp_peer *p, uint16_t remote_port) {
	pthread_mutex_lock(&p->mu);
	if (p->sock != NULL || p->ctx == NULL) {
		pthread_mutex_unlock(&p->mu);
		return -1;
	}
	p->sock = utp_create_socket(p->ctx);
	if (!p->sock) {
		pthread_mutex_unlock(&p->mu);
		return -1;
	}
	p->state = LIBUTP_STATE_CONNECTING;

	struct sockaddr_in to;
	memset(&to, 0, sizeof(to));
	to.sin_family = AF_INET;
	to.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	to.sin_port = htons(remote_port);
	utp_connect(p->sock, (struct sockaddr *)&to, sizeof(to));
	pthread_mutex_unlock(&p->mu);
	wake(p);
	return 0;
}

void libutp_peer_listen(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	p->listening = 1;
	pthread_mutex_unlock(&p->mu);
	wake(p);
}

int libutp_peer_state(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	int s = p->state;
	pthread_mutex_unlock(&p->mu);
	return s;
}

int libutp_peer_error(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	int e = p->err;
	pthread_mutex_unlock(&p->mu);
	return e;
}

long libutp_peer_queue_write(libutp_peer *p, const void *buf, size_t len) {
	pthread_mutex_lock(&p->mu);
	if (buf_append(&p->tx, buf, len) < 0) {
		pthread_mutex_unlock(&p->mu);
		return -1;
	}
	pump_writes(p);
	pthread_mutex_unlock(&p->mu);
	wake(p);
	return (long)len;
}

long libutp_peer_pending_write(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	long n = (long)p->tx.len;
	pthread_mutex_unlock(&p->mu);
	return n;
}

long libutp_peer_read(libutp_peer *p, void *buf, size_t len) {
	pthread_mutex_lock(&p->mu);
	size_t n = p->rx.len < len ? p->rx.len : len;
	if (n > 0) {
		memcpy(buf, p->rx.data, n);
		buf_consume(&p->rx, n);
	}
	pthread_mutex_unlock(&p->mu);
	return (long)n;
}

uint64_t libutp_peer_bytes_received(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	uint64_t n = p->bytes_received;
	pthread_mutex_unlock(&p->mu);
	return n;
}

void libutp_peer_close(libutp_peer *p) {
	pthread_mutex_lock(&p->mu);
	p->want_close = 1;
	pump_writes(p);
	pthread_mutex_unlock(&p->mu);
	wake(p);
}
