package utp

import (
	"time"

	utp "github.com/zen-eth/utp-go"
)

// Option names a libutp context or socket option, for Socket.SetOption. The
// values are libutp's (utp.h).
type Option int32

const (
	LogNormal   Option = 16 // UTP_LOG_NORMAL
	LogMtu      Option = 17 // UTP_LOG_MTU
	LogDebug    Option = 18 // UTP_LOG_DEBUG
	SendBuffer  Option = 19 // UTP_SNDBUF
	RecvBuffer  Option = 20 // UTP_RCVBUF
	TargetDelay Option = 21 // UTP_TARGET_DELAY

	// TimedOut is libutp's UTP_ETIMEDOUT error code.
	TimedOut = 2
)

// contextOptions is libutp's per-context option set, with its defaults
// (utp_api.cpp:74, :91).
type contextOptions struct {
	logNormal, logMtu, logDebug bool
	sndbuf, rcvbuf              int
	targetDelay                 int // microseconds
}

func defaultContextOptions() contextOptions {
	return contextOptions{
		sndbuf:      1024 * 1024,
		rcvbuf:      1024 * 1024,
		targetDelay: 100000,
	}
}

// set is utp_context_set_option (utp_internal.cpp:2640-2673).
func (o *contextOptions) set(opt Option, val int) int {
	switch opt {
	case LogNormal:
		o.logNormal = val != 0
	case LogMtu:
		o.logMtu = val != 0
	case LogDebug:
		o.logDebug = val != 0
	case TargetDelay:
		o.targetDelay = val
	case SendBuffer:
		if val < 1 {
			return -1 // libutp asserts val >= 1
		}
		o.sndbuf = val
	case RecvBuffer:
		if val < 1 {
			return -1
		}
		o.rcvbuf = val
	default:
		return -1
	}
	return 0
}

// get is utp_context_get_option.
func (o *contextOptions) get(opt Option) int {
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	switch opt {
	case LogNormal:
		return b(o.logNormal)
	case LogMtu:
		return b(o.logMtu)
	case LogDebug:
		return b(o.logDebug)
	case TargetDelay:
		return o.targetDelay
	case SendBuffer:
		return o.sndbuf
	case RecvBuffer:
		return o.rcvbuf
	}
	return -1
}

// connectionConfig is what a connection made now gets: libutp copies the
// context's options into each socket it creates (utp_internal.cpp:2616-2621).
func (o *contextOptions) connectionConfig() *utp.ConnectionConfig {
	c := utp.NewConnectionConfig()
	c.WindowSize = uint32(o.sndbuf)
	c.BufferSize = o.rcvbuf
	target := o.targetDelay
	if target <= 0 {
		target = 100000 // apply_ccontrol's own fallback (:1633-1634)
	}
	c.TargetDelay = time.Duration(target) * time.Microsecond
	return c
}
