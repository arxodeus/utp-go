package anacrolix

import (
	"github.com/zen-eth/utp-go/utpnet"
)

// The claim this module exists to check: a utpnet.Socket satisfies the
// interface torrent requires of a uTP implementation.
//
// If torrent's interface gains a method, or changes one, this stops
// compiling. That is the point -- an assertion in a comment would not.
var _ UtpSocket = (*utpnet.Socket)(nil)

// Not covered here, and worth saying plainly:
//
//   - The firewall callback. torrent uses it to refuse connections from
//     blocked addresses before any state is created for them. This library
//     has no equivalent hook, so NewUtpSocket accepts one and ignores it. A
//     caller relying on IP blocking would not get it, which is why this is
//     stated rather than left to be discovered.
//   - Holepunching, which torrent drives through its own dialer rather than
//     through this interface.
