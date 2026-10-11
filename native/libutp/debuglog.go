//go:build cgo && libutp_debuglog

package libutp

// Built with the libutp_debuglog tag, libutp is compiled with its debug log
// lines, so that Driver.EnableWindowLog can read the congestion window from
// them. See there for why it is not the default.

// #cgo CXXFLAGS: -DUTP_DEBUG_LOGGING=1
import "C"
