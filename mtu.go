package utp

import "time"

// Path MTU discovery.
//
// A sender must choose a datagram size without being told what the path
// carries. Too small wastes header overhead; too large is worse, since a
// datagram above the path MTU is either fragmented or, on IPv6 and on IPv4
// with don't-fragment set, silently dropped, and a connection that cannot
// detect that stops.
//
// The search is a binary search between a floor known to work and a ceiling
// known or assumed not to. Each probe is an ordinary data packet sized at the
// midpoint and sent with fragmentation disabled where the platform allows:
//
//   - acknowledged, the floor rises to the probe's size;
//   - lost, the ceiling drops to just below it.
//
// The search ends when the two are within mtuSearchDoneThreshold, and
// restarts every mtuSearchInterval because paths change. A probe is detected
// lost by a retransmission timeout or by duplicate acknowledgements
// (connection.noteDuplicateAck); only the latter works while the send window
// is full.

const (
	// mtuAbsoluteFloor is the smallest datagram the search settles on: 576,
	// IPv4's guaranteed reassembly size.
	mtuAbsoluteFloor uint32 = 576

	// mtuSearchDoneThreshold is how close the floor and ceiling must be for
	// the search to stop. The search settles on the floor, the only size
	// known to get through.
	mtuSearchDoneThreshold uint32 = 16

	// mtuSearchInterval is how long a completed search stands before it is
	// redone, since a path's MTU can change in either direction.
	mtuSearchInterval = 30 * time.Minute

	// mtuHeaderOverhead is the uTP header a data packet carries before its
	// payload. Data packets carry no selective ack (see statePacket).
	mtuHeaderOverhead uint32 = minimalHeaderSize
)

// mtuSearch is one connection's path-MTU state.
type mtuSearch struct {
	// floor is the largest datagram size known to have reached the peer.
	floor uint32
	// ceiling is the smallest size believed not to, or the starting
	// assumption if nothing has failed yet.
	ceiling uint32
	// current is the size being used now: the midpoint while searching, the
	// floor once the search has converged.
	current uint32

	// probeSeq and probeSize identify the packet under test. A probe is
	// outstanding only while probing is true.
	probeSeq  uint16
	probeSize uint32
	probing   bool

	// nextSearch is when a converged search is redone.
	nextSearch time.Time
}

// newMtuSearch starts a search bounded above by ceiling, sending at the
// ceiling. The first packet larger than the floor is the probe; if it is
// lost the ceiling comes down and the search continues from the midpoint.
// The default ceiling of 1402 bytes (1232 for IPv6, or less if the local
// interface says so) leaves room for a tunnel.
func newMtuSearch(ceiling uint32, now time.Time) *mtuSearch {
	m := &mtuSearch{}
	m.reset(ceiling, now)
	m.current = m.ceiling
	return m
}

// reset restarts the search.
func (m *mtuSearch) reset(ceiling uint32, now time.Time) {
	if ceiling < mtuAbsoluteFloor {
		// A request below the floor gets the floor.
		ceiling = mtuAbsoluteFloor
	}
	m.floor = mtuAbsoluteFloor
	m.ceiling = ceiling
	m.probing = false
	m.probeSeq = 0
	m.probeSize = 0
	m.searchUpdate(now)
}

// research restarts a converged search after mtuSearchInterval without
// giving up the size it settled on: the floor and ceiling are restored, but
// packets stay at the size known to get through, and the next one is the
// probe. reset would drop the size to the midpoint.
func (m *mtuSearch) research(ceiling uint32, now time.Time) {
	known := m.current
	m.reset(ceiling, now)
	if known > m.floor && known <= m.ceiling {
		m.current = known
	}
}

// searchUpdate takes one binary-search step. Called whenever the floor or
// ceiling moves.
func (m *mtuSearch) searchUpdate(now time.Time) {
	if m.floor > m.ceiling {
		m.floor = m.ceiling
	}
	m.current = (m.floor + m.ceiling) / 2

	// A new probe may be sent.
	m.probing = false
	m.probeSeq = 0
	m.probeSize = 0

	if m.ceiling-m.floor <= mtuSearchDoneThreshold {
		// Settle on the floor, the only size known to get through.
		m.current = m.floor
		m.ceiling = m.floor
		m.nextSearch = now.Add(mtuSearchInterval)
	}
}

// icmpFragmentationNeeded applies an ICMP "fragmentation needed" report to
// the search.
//
// linkMTU is the next-hop MTU the router quoted: the largest IP datagram the
// next hop carries. Zero, or a value outside [576, 0x2000), means the router
// gave no usable size, and the gap is halved for a probe to resolve.
//
// A usable figure is converted from a link MTU to a uTP datagram size by
// subtracting the IP and UDP headers, then lowers the ceiling, and the next
// packet is sent at that size.
func (m *mtuSearch) icmpFragmentationNeeded(linkMTU uint32, now time.Time) {
	if usable, ok := udpPayloadForLinkMTU(linkMTU); ok {
		if usable < m.ceiling {
			m.ceiling = usable
		}
		m.searchUpdate(now)
		// Test the router's figure itself rather than the midpoint below it:
		// a smaller hop may lie further on, but this one is known.
		if m.ceiling >= m.floor {
			m.current = m.ceiling
		}
		return
	}
	// No usable figure: halve the gap and let a probe decide.
	m.ceiling = (m.floor + m.ceiling) / 2
	m.searchUpdate(now)
}

// ipv4HeaderAndUDPOverhead is the 20-byte IPv4 header plus 8 bytes of UDP.
// On an IPv6 path the result is up to 20 bytes too generous; a probe at that
// size then fails and lowers the ceiling.
const ipv4HeaderAndUDPOverhead = 28

// ipv6HeaderAndUDPOverhead is the 40-byte IPv6 header plus 8 bytes of UDP,
// used where the address family is known, as in path_mtu.go.
const ipv6HeaderAndUDPOverhead = 48

// udpPayloadForLinkMTU converts a router's next-hop MTU into the uTP datagram
// size that fits in it, and reports whether the figure is usable: at least
// 576, IPv4's guaranteed reassembly size, and below 0x2000.
func udpPayloadForLinkMTU(linkMTU uint32) (uint32, bool) {
	if linkMTU < 576 || linkMTU >= 0x2000 {
		return 0, false
	}
	return linkMTU - ipv4HeaderAndUDPOverhead, true
}

// done reports whether the search has converged.
func (m *mtuSearch) done() bool { return m.ceiling <= m.floor }

// dueForSearch reports whether a converged search has stood long enough to be
// redone.
func (m *mtuSearch) dueForSearch(now time.Time) bool {
	return m.done() && !m.nextSearch.IsZero() && !now.Before(m.nextSearch)
}

// payloadSize returns how many bytes of application data a packet may carry.
func (m *mtuSearch) payloadSize() uint32 {
	if m.current <= mtuHeaderOverhead {
		return 1
	}
	return m.current - mtuHeaderOverhead
}

// eligibleProbe reports whether a packet of this datagram size and sequence
// number should be the next probe: the search is open, the packet is larger
// than the floor and no larger than the ceiling, no probe is outstanding,
// and this is a first transmission. A retransmission is never a probe.
func (m *mtuSearch) eligibleProbe(size uint32, firstTransmission bool) bool {
	return !m.done() &&
		!m.probing &&
		firstTransmission &&
		size > m.floor &&
		size <= m.ceiling
}

// beginProbe records that a packet is under test.
func (m *mtuSearch) beginProbe(seq uint16, size uint32) {
	m.probing = true
	m.probeSeq = seq
	m.probeSize = size
}

// onAck records a packet acknowledged. If it was the probe, the floor rises
// to its size.
func (m *mtuSearch) onAck(seq uint16, now time.Time) bool {
	if !m.probing || seq != m.probeSeq {
		return false
	}
	m.floor = m.probeSize
	m.searchUpdate(now)
	return true
}

// onProbeLost records the probe as lost, so the ceiling drops to just below
// its size. It is called on a retransmission timeout with the probe the only
// packet outstanding, and on a third duplicate acknowledgement of the packet
// before the probe (connection.noteDuplicateAck). Neither is a congestion
// signal, so the window is not reduced.
func (m *mtuSearch) onProbeLost(now time.Time) bool {
	if !m.probing {
		return false
	}
	if m.probeSize > 0 {
		m.ceiling = m.probeSize - 1
	}
	m.searchUpdate(now)
	return true
}

// clearProbe forgets the outstanding probe without drawing a conclusion, so
// another can be sent. The ceiling comes down only when the probe was the
// only packet outstanding, since only then does its loss indicate size rather
// than congestion; but a probe lost alongside others is still gone, and
// waiting for it would stall the search.
func (m *mtuSearch) clearProbe() {
	m.probing = false
	m.probeSeq = 0
	m.probeSize = 0
}

// probeOutstanding reports whether seq is the packet currently under test.
func (m *mtuSearch) probeOutstanding(seq uint16) bool {
	return m.probing && m.probeSeq == seq
}
