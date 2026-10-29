package anacrolix

import (
	"github.com/arxodeus/utp/utpnet"
)

// A utpnet.Socket satisfies the interface torrent requires of a uTP
// implementation. TestSatisfiesTorrentsUTPSocketInterface checks the same
// against torrent's own interface. Holepunching, which torrent drives through
// its own dialer, is not covered.
var _ Socket = (*utpnet.Socket)(nil)
