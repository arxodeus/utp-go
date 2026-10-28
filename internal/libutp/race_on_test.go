//go:build race && cgo

package libutp_test

// raceEnabled reports whether the test binary was built with -race. The race
// detector instruments this library and not libutp's C, so a comparison of
// the two's timing measures the detector; those gates report instead.
const raceEnabled = true
