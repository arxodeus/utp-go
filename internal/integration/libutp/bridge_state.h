// bridge_state.h -- connection states, shared by the socket-backed peer and
// the deterministic driver so Go sees one set of constants.

#ifndef LIBUTP_BRIDGE_STATE_H
#define LIBUTP_BRIDGE_STATE_H

enum {
	LIBUTP_STATE_IDLE = 0,
	LIBUTP_STATE_CONNECTING = 1,
	LIBUTP_STATE_CONNECTED = 2,
	LIBUTP_STATE_EOF = 3,
	LIBUTP_STATE_DESTROYED = 4,
	LIBUTP_STATE_ERROR = 5
};

#endif // LIBUTP_BRIDGE_STATE_H
