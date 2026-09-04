//go:build race

package integrated

// raceEnabled reports whether the test binary was built with -race.
// The race detector slows this package's transfers down by roughly an order
// of magnitude, so time budgets are scaled rather than left to fail
// spuriously.
const raceEnabled = true
