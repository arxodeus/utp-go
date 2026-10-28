//go:build race

package netem

// raceEnabled reports whether the test binary was built with -race. The race
// detector instruments this library and not libutp's C, and slows the
// emulated network's Go side by an order of magnitude; a test whose reference
// is libutp's timing over it measures the detector.
const raceEnabled = true
