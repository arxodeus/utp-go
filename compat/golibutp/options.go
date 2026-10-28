package utp

import (
	"time"

	utp "github.com/arxodeus/utp"
)

// Option identifies a context or socket option for Socket.SetOption. The
// values match go-libutp's.
type Option int32

const (
	LogNormal   Option = 16 // UTP_LOG_NORMAL
	LogMtu      Option = 17 // UTP_LOG_MTU
	LogDebug    Option = 18 // UTP_LOG_DEBUG
	SendBuffer  Option = 19 // UTP_SNDBUF
	RecvBuffer  Option = 20 // UTP_RCVBUF
	TargetDelay Option = 21 // UTP_TARGET_DELAY

	// TimedOut is the error code reported for a connection that timed out.
	TimedOut = 2
)

// contextOptions holds a Socket's options and their defaults.
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

// set stores an option value and reports the result SetOption returns.
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
			return -1 // A buffer must hold at least one byte.
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

// get returns an option's current value.
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

// connectionConfig returns the configuration for a connection created now.
func (o *contextOptions) connectionConfig() *utp.ConnectionConfig {
	c := utp.NewConnectionConfig()
	c.WindowSize = uint32(o.sndbuf)
	c.BufferSize = o.rcvbuf
	target := o.targetDelay
	if target <= 0 {
		target = 100000 // Fall back to the default target delay.
	}
	c.TargetDelay = time.Duration(target) * time.Microsecond
	return c
}
