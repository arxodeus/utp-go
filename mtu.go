package utp_go

import "time"

// Path MTU discovery, as libutp performs it.
//
// A uTP sender has to choose a datagram size without being told what the path
// carries. Too small wastes header overhead on every packet; too large is
// worse than wasteful, because a datagram above the path's MTU is either
// fragmented -- costing more than it saves -- or, on IPv6 and on any IPv4 path
// with the don't-fragment bit set, silently dropped. A connection that picks
// too large a size and cannot detect it does not run slowly; it stops.
//
// That is why this library shipped a fixed 1024 rather than libutp's ~1400:
// without discovery, the only safe fixed size is a small one. Measured, the
// difference is +2.4% on a long transfer and +17% on a reordering profile --
// worth having, but not worth a connection that fails outright on a PPPoE or
// VPN path. Discovery is what makes the larger size safe to reach for.
//
// The algorithm is a binary search between a floor known to work and a ceiling
// known or assumed not to. Each probe is an ordinary data packet, sized at the
// midpoint and sent with fragmentation disabled where the platform allows it:
//
//   - acknowledged, and the floor rises to the probe's size;
//   - lost, and the ceiling drops to just below it.
//
// The search ends when the two are within mtuSearchDoneThreshold of each
// other, and restarts every mtuSearchInterval because paths change.
//
// libutp: mtu_search_update (utp_internal.cpp:1289-1312), mtu_reset
// (:1314-1322), the probe decision in send_packet (:890-925), the
// probe-acknowledged path (:1969-1974), and the two failure paths --
// retransmission timeout (:1152-1167) and duplicate acknowledgements
// (:1927-1940).

const (
	// mtuAbsoluteFloor is the smallest datagram the search will settle on.
	//
	// libutp: `mtu_floor = 576;` with the comment "Less would not pass TCP..."
	// (utp_internal.cpp:1318). 576 is IPv4's guaranteed reassembly size, so a
	// path that cannot carry it cannot carry much of anything.
	mtuAbsoluteFloor uint32 = 576

	// mtuSearchDoneThreshold is how close the floor and ceiling must be for
	// the search to stop. libutp: `if (mtu_ceiling - mtu_floor <= 16)`
	// (utp_internal.cpp:1303).
	//
	// Stopping short is deliberate: the last 16 bytes are not worth the
	// round trips, and the search settles on the *floor*, the only size known
	// to have made it through.
	mtuSearchDoneThreshold uint32 = 16

	// mtuSearchInterval is how long a completed search stands before the
	// whole thing is redone. libutp: 30 minutes (utp_internal.cpp:1310,
	// :1320). Paths change; a route that shortened is worth finding, and one
	// that lengthened must be found.
	mtuSearchInterval = 30 * time.Minute

	// mtuHeaderOverhead is what a uTP packet spends before payload: the
	// 20-byte header plus room for a selective-ack extension.
	//
	// libutp subtracts only `sizeof(PacketFormatV1)` (utp_internal.cpp:1759),
	// because it appends the extension to a separately sized buffer. This
	// library builds one buffer, so the extension has to be accounted for
	// here or a packet carrying one would exceed the discovered size -- which
	// is exactly the packet that must not.
	mtuHeaderOverhead uint32 = MINIMAL_HEADER_SIZE + EXTENSION_TYPE_LEN +
		EXTENSION_LEN_LEN + SELECTIVE_ACK_BITS/8
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

	// probeSeq and probeSize identify the packet currently under test. A
	// probe is outstanding only while probing is true; libutp uses
	// `mtu_probe_seq == 0` as the same flag, which costs it sequence number
	// zero (see its `seq_nr != 1` guard at utp_internal.cpp:910).
	probeSeq  uint16
	probeSize uint32
	probing   bool

	// nextSearch is when a converged search is redone.
	nextSearch time.Time
}

// newMtuSearch starts a search bounded above by ceiling.
//
// The initial size is the midpoint, not the ceiling: an untested path gets a
// conservative packet that grows only once a probe has come back
// acknowledged. That ordering is the whole safety argument for discovery --
// nothing large is sent until something large is known to arrive.
func newMtuSearch(ceiling uint32, now time.Time) *mtuSearch {
	m := &mtuSearch{}
	m.reset(ceiling, now)
	return m
}

// reset restarts the search. libutp's mtu_reset (utp_internal.cpp:1314-1322).
func (m *mtuSearch) reset(ceiling uint32, now time.Time) {
	if ceiling < mtuAbsoluteFloor {
		// A caller asking for less than the floor gets the floor: below it
		// the search has nothing to search.
		ceiling = mtuAbsoluteFloor
	}
	m.floor = mtuAbsoluteFloor
	m.ceiling = ceiling
	m.probing = false
	m.probeSeq = 0
	m.probeSize = 0
	m.searchUpdate(now)
}

// searchUpdate takes one binary-search step. Called whenever the floor or
// ceiling moves. libutp's mtu_search_update (utp_internal.cpp:1289-1312).
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
		// Settle on the floor: the only size known to have gone through.
		m.current = m.floor
		m.ceiling = m.floor
		m.nextSearch = now.Add(mtuSearchInterval)
	}
}

// done reports whether the search has converged.
func (m *mtuSearch) done() bool { return m.ceiling <= m.floor }

// dueForSearch reports whether a converged search has stood long enough to be
// redone.
func (m *mtuSearch) dueForSearch(now time.Time) bool {
	return m.done() && !m.nextSearch.IsZero() && !now.Before(m.nextSearch)
}

// payloadSize is how many bytes of application data a packet may carry.
func (m *mtuSearch) payloadSize() uint32 {
	if m.current <= mtuHeaderOverhead {
		return 1
	}
	return m.current - mtuHeaderOverhead
}

// eligibleProbe reports whether a packet of this datagram size, carrying this
// sequence number, should be used as the next probe.
//
// libutp's conditions (utp_internal.cpp:906-911): the search is still open,
// the packet is larger than the floor and no larger than the ceiling, no
// probe is outstanding, and this is a first transmission. A retransmission is
// never a probe -- libutp notes that an oversized packet being resent needs to
// fragment just to get through, which is the opposite of what a probe is for.
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

// onAck reports a packet acknowledged. If it was the probe, the floor rises to
// its size. libutp: utp_internal.cpp:1969-1974.
func (m *mtuSearch) onAck(seq uint16, now time.Time) bool {
	if !m.probing || seq != m.probeSeq {
		return false
	}
	m.floor = m.probeSize
	m.searchUpdate(now)
	return true
}

// onProbeLost reports the probe presumed lost, so the ceiling drops to just
// below its size.
//
// libutp reaches this two ways: a retransmission timeout where the probe was
// the only packet outstanding (utp_internal.cpp:1152-1167), and a third
// duplicate acknowledgement pointing at the packet before the probe
// (:1927-1934). Both mean the same thing -- a packet of that size did not
// arrive -- and neither is a congestion signal, which is why libutp sets
// `ignore_loss` on the timeout path and does not shrink the window.
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

// probeOutstanding reports whether seq is the packet currently under test.
func (m *mtuSearch) probeOutstanding(seq uint16) bool {
	return m.probing && m.probeSeq == seq
}
