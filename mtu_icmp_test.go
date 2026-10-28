package utp

import (
	"testing"
	"time"
)

// A router's next-hop figure is used exactly, saving the round trip that
// losing a probe would cost to discover the same limit.
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
	// The next size sent is the ceiling itself rather than the midpoint
	// below it.
	if m.current != m.ceiling {
		t.Errorf("current %d, want the ceiling %d: the router's own figure is what should be tried next",
			m.current, m.ceiling)
	}
	if m.probing {
		t.Error("a probe is still marked outstanding; mtu_search_update clears it")
	}
}

// The ceiling only comes down: a router quoting something larger than what
// is already known does not raise it.
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

// Routers that predate RFC 1191 send zero, and figures outside the plausible
// range are not trusted. The fallback halves the gap so a probe can decide.
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

// The bounds: 576 is IPv4's guaranteed reassembly size and the lowest figure
// acted on; at 0x2000 and above the router is not believed.
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

// The router quotes a link MTU, so the IP and UDP headers are subtracted: a
// datagram of the size the search settles on fits inside the link the router
// named, headers included.
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

// An ICMP report that lands within the convergence threshold ends the search
// rather than leaving it probing.
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

// A figure below the floor does not invert the search, which would send
// packets the path just said it cannot carry. The search may settle below
// 576 bytes, since that is what the link actually carries.
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

// Against a path that silently drops anything over 1300 bytes but whose
// router reports it, the search reaches a usable size without losing probes.
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
