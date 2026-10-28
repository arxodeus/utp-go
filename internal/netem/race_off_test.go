//go:build !race

package netem

// raceEnabled reports whether the test binary was built with -race.
const raceEnabled = false
