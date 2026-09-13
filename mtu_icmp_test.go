package utp_go

import (
	"testing"
	"time"
)

// A router that says how big the next hop is should be believed, and believed
// exactly: the point of honouring ICMP at all is to skip the round trip that
// discovering the same limit by losing a probe would cost.
func TestMtuIcmpFragmentationNeededUsesTheRoutersFigure(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1472, now)
	before := m.ceiling

	// A 1300-byte link carries 1300-20-8 = 1272 bytes of uTP datagram.
	m.icmpFragmentationNeeded(1300, now)

	if m.ceiling != 1272 {
		t.Errorf("ceiling %d, want 1272 (a 1300-byte link less the IPv4 and UDP headers)", m.ceiling)
	}
	if m.ceiling >= before {
		t.Errorf("ceiling did not come down: %d then %d", before, m.ceiling)
	}
	// libutp is explicit that this is the one case where the next size sent
	// is the ceiling rather than the midpoint below it (utp_internal.cpp:3088-3093).
	if m.current != m.ceiling {
		t.Errorf("current %d, want the ceiling %d: the router's own figure is what should be tried next",
			m.current, m.ceiling)
	}
	if m.probing {
		t.Error("a probe is still marked outstanding; mtu_search_update clears it")
	}
}

// The ceiling only ever comes down. A router quoting something larger than
// what is already known must not raise it -- libutp's min().
func TestMtuIcmpFragmentationNeededNeverRaisesTheCeiling(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1472, now)
	// Bring the ceiling down first, the ordinary way.
	m.ceiling = 1000
	m.searchUpdate(now)

	m.icmpFragmentationNeeded(9000, now)

	if m.ceiling > 1000 {
		t.Errorf("ceiling rose to %d from 1000 on a router's larger figure", m.ceiling)
	}
}

// "It might not be initialized or sent properly": routers predating RFC 1191
// send zero, and anything outside libutp's window is not to be trusted. The
// fallback is the binary search, with the gap halved so a probe finds the
// truth.
func TestMtuIcmpFragmentationNeededFallsBackWhenTheFigureIsUnusable(t *testing.T) {
	for _, linkMTU := range []uint16{0, 1, 575, 0x2000, 0xffff} {
		t.Run(sizeName(uint32(linkMTU)), func(t *testing.T) {
			now := time.Now()
			m := newMtuSearch(1472, now)
			floor, ceiling := m.floor, m.ceiling
			want := (floor + ceiling) / 2

			m.icmpFragmentationNeeded(uint32(linkMTU), now)

			if m.ceiling != want {
				t.Errorf("ceiling %d, want the midpoint %d of floor %d and ceiling %d",
					m.ceiling, want, floor, ceiling)
			}
			if m.current > m.ceiling {
				t.Errorf("current %d above ceiling %d", m.current, m.ceiling)
			}
		})
	}
}

// The bounds themselves, stated once rather than inferred from the search's
// behaviour. 576 is IPv4's guaranteed reassembly size and the lowest libutp
// will act on; 0x2000 is where it stops believing the router.
func TestUdpPayloadForLinkMTUBounds(t *testing.T) {
	cases := []struct {
		link uint32
		want uint32
		ok   bool
	}{
		{0, 0, false},
		{575, 0, false},
		{576, 576 - 28, true},
		{1280, 1280 - 28, true}, // IPv6's minimum
		{1500, 1500 - 28, true}, // Ethernet
		{0x1fff, 0x1fff - 28, true},
		{0x2000, 0, false},
	}
	for _, c := range cases {
		got, ok := udpPayloadForLinkMTU(c.link)
		if ok != c.ok || got != c.want {
			t.Errorf("udpPayloadForLinkMTU(%d) = (%d, %v), want (%d, %v)",
				c.link, got, ok, c.want, c.ok)
		}
	}
}

// The conversion is the one deliberate difference from libutp, so assert what
// it buys: a datagram of the size the search settles on must fit inside the
// link the router named, headers included. libutp's figure does not -- it is
// 28 bytes over, and the packet it sends next is dropped by the same router.
func TestMtuIcmpFragmentationNeededLeavesRoomForTheHeaders(t *testing.T) {
	const linkMTU = 1400
	now := time.Now()
	m := newMtuSearch(1472, now)
	m.icmpFragmentationNeeded(linkMTU, now)

	datagram := m.current + ipv4HeaderAndUDPOverhead
	if datagram > linkMTU {
		t.Errorf("next datagram would be %d bytes on the wire over a %d-byte link", datagram, linkMTU)
	}
	// And not needlessly small: within one header's worth of the limit.
	if linkMTU-datagram > ipv4HeaderAndUDPOverhead {
		t.Errorf("next datagram is %d bytes on a %d-byte link, %d bytes of headroom wasted",
			datagram, linkMTU, linkMTU-datagram)
	}
}

// An ICMP report that lands inside the convergence threshold ends the search
// rather than leaving it probing forever: the same mtu_search_update tail the
// probe path uses.
func TestMtuIcmpFragmentationNeededCanFinishTheSearch(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1472, now)
	// Raise the floor to just under the figure the router is about to give.
	m.floor = 1272 - 8
	m.icmpFragmentationNeeded(1300, now)

	if !m.done() {
		t.Errorf("search not done with floor %d ceiling %d, a gap of %d within the threshold of %d",
			m.floor, m.ceiling, m.ceiling-m.floor, mtuSearchDoneThreshold)
	}
	if m.current > m.ceiling {
		t.Errorf("current %d above ceiling %d", m.current, m.ceiling)
	}
}

// A figure below the floor must not invert the search. libutp asserts
// mtu_floor <= mtu_ceiling and then computes a midpoint that would sit above
// the ceiling; with assertions off, that sends packets the path just said it
// cannot carry.
//
// It also settles below the 576-byte floor, which libutp never does: its
// floor is 576 of UDP payload and its ceiling is the router's link MTU, so on
// a link this narrow it keeps sending 576-byte payloads into a hop that
// carries 572. Going below the floor here is the consequence of converting
// the router's figure honestly, and 572 is what the link actually carries.
// The floor is a heuristic -- "less would not pass TCP" -- not a guarantee.
func TestMtuIcmpFragmentationNeededBelowTheFloor(t *testing.T) {
	const linkMTU = 600
	now := time.Now()
	m := newMtuSearch(1472, now)
	m.floor = 1200
	m.ceiling = 1400

	m.icmpFragmentationNeeded(linkMTU, now)

	if m.floor > m.ceiling {
		t.Fatalf("search inverted: floor %d above ceiling %d", m.floor, m.ceiling)
	}
	if m.current > m.ceiling {
		t.Errorf("current %d above ceiling %d", m.current, m.ceiling)
	}
	if got := m.current + ipv4HeaderAndUDPOverhead; got > linkMTU {
		t.Errorf("next datagram would be %d bytes on the wire over a %d-byte link", got, linkMTU)
	}
	if m.current != linkMTU-ipv4HeaderAndUDPOverhead {
		t.Errorf("current %d, want %d: what a %d-byte link carries",
			m.current, linkMTU-ipv4HeaderAndUDPOverhead, linkMTU)
	}
}

// What the mechanism is for, stated as a saving: against a path that silently
// drops anything over 1300 bytes but whose router says so, the ICMP-informed
// search reaches a usable size without losing probes to find it.
func TestMtuIcmpFragmentationNeededSavesProbes(t *testing.T) {
	const linkMTU = 1300
	const truth = linkMTU - ipv4HeaderAndUDPOverhead

	probe := func(m *mtuSearch, useICMP bool) (rounds, lost int) {
		now := time.Now()
		for !m.done() && rounds < 64 {
			rounds++
			size := m.current
			m.beginProbe(uint16(rounds), size)
			if size <= truth {
				m.onAck(uint16(rounds), now)
				continue
			}
			lost++
			if useICMP {
				// The router that dropped it told us why.
				m.icmpFragmentationNeeded(linkMTU, now)
			} else {
				m.onProbeLost(now)
			}
		}
		return rounds, lost
	}

	now := time.Now()
	blind := newMtuSearch(1472, now)
	blindRounds, blindLost := probe(blind, false)
	informed := newMtuSearch(1472, now)
	informedRounds, informedLost := probe(informed, true)

	t.Logf("blind: %d rounds, %d lost; ICMP-informed: %d rounds, %d lost",
		blindRounds, blindLost, informedRounds, informedLost)

	if informed.current > truth {
		t.Errorf("informed search settled on %d, above the path's %d", informed.current, truth)
	}
	if informedLost >= blindLost {
		t.Errorf("ICMP saved nothing: %d probes lost informed against %d blind", informedLost, blindLost)
	}
	if informedRounds > blindRounds {
		t.Errorf("ICMP cost rounds: %d against %d", informedRounds, blindRounds)
	}
}
