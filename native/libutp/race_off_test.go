//go:build !race && cgo

package libutp_test

// raceEnabled reports whether the test binary was built with -race.
const raceEnabled = false
