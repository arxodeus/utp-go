package utp_go

import (
	"strconv"
	"testing"
	"time"
)

// The search must converge on a size the path carries, never above it, and
// then stop. Simulated against a range of true path MTUs, including the
// awkward ones.
func TestMtuSearchConverges(t *testing.T) {
	for _, truth := range []uint32{576, 577, 900, 1280, 1400, 1492, 1500, 9000} {
		truth := truth
		t.Run(sizeName(truth), func(t *testing.T) {
			now := time.Now()
			// The ceiling starts optimistic, above the real path.
			m := newMtuSearch(9000, now)

			const maxRounds = 64
			rounds := 0
			for !m.done() && rounds < maxRounds {
				rounds++
				size := m.current
				if !m.eligibleProbe(size, true) {
					t.Fatalf("round %d: size %d is not an eligible probe with floor %d ceiling %d",
						rounds, size, m.floor, m.ceiling)
				}
				m.beginProbe(uint16(rounds), size)

				// The path carries anything up to truth.
				if size <= truth {
					m.onAck(uint16(rounds), now)
				} else {
					m.onProbeLost(now)
				}

				if m.current > m.ceiling {
					t.Fatalf("round %d: current %d above ceiling %d", rounds, m.current, m.ceiling)
				}
			}

			if !m.done() {
				t.Fatalf("did not converge in %d rounds: floor %d ceiling %d", maxRounds, m.floor, m.ceiling)
			}
			if rounds > 20 {
				t.Errorf("took %d rounds to converge; a binary search over 9000 bytes needs about 14", rounds)
			}

			// Never above the truth: that is the whole safety property.
			if m.current > truth {
				t.Errorf("settled on %d, above the path's %d -- every packet would be dropped",
					m.current, truth)
			}
			// And close enough below it to be worth the search.
			if truth-m.current > mtuSearchDoneThreshold+1 {
				t.Errorf("settled on %d for a path of %d, %d bytes short; the search stops within %d",
					m.current, truth, truth-m.current, mtuSearchDoneThreshold)
			}
			t.Logf("path %d: converged on %d in %d rounds", truth, m.current, rounds)
		})
	}
}

// A path that carries less than the floor still yields a usable size rather
// than a nonsensical one.
func TestMtuSearchBelowFloor(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(400, now)
	if m.current < mtuAbsoluteFloor {
		t.Errorf("current %d below the absolute floor %d", m.current, mtuAbsoluteFloor)
	}
	if got := m.payloadSize(); got == 0 {
		t.Error("payload size is zero; nothing could ever be sent")
	}
}

// The payload never exceeds the discovered datagram size, header and a
// selective-ack extension included. A packet that overruns is exactly the one
// discovery exists to prevent.
func TestMtuPayloadLeavesRoomForHeaderAndExtension(t *testing.T) {
	now := time.Now()
	for _, ceiling := range []uint32{600, 1000, 1400, 1500} {
		m := newMtuSearch(ceiling, now)
		for i := 0; i < 32 && !m.done(); i++ {
			m.beginProbe(uint16(i), m.current)
			m.onAck(uint16(i), now)
		}

		payload := m.payloadSize()
		// One full packet: header, a four-byte selective ack with its two
		// framing bytes, and the payload.
		total := payload + mtuHeaderOverhead
		if total > m.current {
			t.Errorf("ceiling %d: a full packet is %d bytes against a discovered MTU of %d",
				ceiling, total, m.current)
		}
	}
}

// Probe eligibility, which is what stops the search sending a probe it cannot
// learn anything from. libutp: utp_internal.cpp:906-911.
func TestMtuProbeEligibility(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1500, now)

	if m.eligibleProbe(m.floor, true) {
		t.Error("a packet the size of the floor was accepted as a probe; it tests nothing")
	}
	if m.eligibleProbe(m.ceiling+1, true) {
		t.Error("a packet above the ceiling was accepted as a probe")
	}
	if !m.eligibleProbe(m.current, true) {
		t.Error("the current size should be an eligible probe")
	}
	if m.eligibleProbe(m.current, false) {
		t.Error("a retransmission was accepted as a probe; libutp excludes them because an " +
			"oversized packet being resent needs to fragment just to get through")
	}

	m.beginProbe(1, m.current)
	if m.eligibleProbe(m.current, true) {
		t.Error("a second probe was accepted while one is outstanding")
	}

	// Once resolved, another may go out.
	m.onAck(1, now)
	if !m.done() && !m.eligibleProbe(m.current, true) {
		t.Error("no probe allowed after the last one was resolved")
	}
}

// A converged search is redone after the interval, because paths change.
// libutp: 30 minutes (utp_internal.cpp:1310).
func TestMtuSearchIsRedoneAfterTheInterval(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1500, now)
	for i := 0; i < 32 && !m.done(); i++ {
		m.beginProbe(uint16(i), m.current)
		m.onAck(uint16(i), now)
	}
	if !m.done() {
		t.Fatal("search did not converge")
	}
	settled := m.current

	if m.dueForSearch(now.Add(mtuSearchInterval - time.Minute)) {
		t.Error("search came due before its interval had passed")
	}
	if !m.dueForSearch(now.Add(mtuSearchInterval)) {
		t.Errorf("search never came due again; a path that changed would never be rediscovered")
	}

	m.reset(1500, now.Add(mtuSearchInterval))
	if m.done() {
		t.Error("reset left the search already finished")
	}
	if m.current >= settled && settled > mtuAbsoluteFloor {
		t.Logf("note: reset restarts from the midpoint %d, below the previously settled %d",
			m.current, settled)
	}
}

// A probe lost lowers the ceiling below it, and never below the floor.
func TestMtuProbeLostLowersCeiling(t *testing.T) {
	now := time.Now()
	m := newMtuSearch(1500, now)
	probe := m.current

	m.beginProbe(7, probe)
	if !m.onProbeLost(now) {
		t.Fatal("losing the outstanding probe was not registered")
	}
	if m.ceiling >= probe {
		t.Errorf("ceiling %d is not below the lost probe's %d", m.ceiling, probe)
	}
	if m.floor > m.ceiling {
		t.Errorf("floor %d rose above ceiling %d", m.floor, m.ceiling)
	}

	// Losing a packet that was not the probe changes nothing.
	before := m.ceiling
	m.beginProbe(9, m.current)
	m.onAck(11, now) // a different packet
	if m.ceiling != before {
		t.Error("an unrelated acknowledgement moved the ceiling")
	}
}

func sizeName(n uint32) string {
	switch n {
	case 576:
		return "576-ipv4-minimum"
	case 1280:
		return "1280-ipv6-minimum"
	case 1492:
		return "1492-pppoe"
	case 1500:
		return "1500-ethernet"
	case 9000:
		return "9000-jumbo"
	default:
		return "path-" + strconv.Itoa(int(n))
	}
}

// The connection actually uses what the search discovers, and grows only
// after a probe comes back acknowledged.
//
// This is the safety argument for raising the ceiling to 1400 stated as a
// test: an untested path gets a conservative packet, and a larger one goes
// out only once a packet of that size has been proven to arrive.
func TestConnectionStartsConservativeAndGrows(t *testing.T) {
	cfg := NewConnectionConfig()

	// The size a fresh connection would send, before any probe is answered.
	initial := newMtuSearch(uint32(cfg.MaxPacketSize), time.Now())
	if initial.current >= uint32(cfg.MaxPacketSize) {
		t.Errorf("a fresh connection starts at %d, the full ceiling of %d -- it should start at "+
			"the midpoint and grow only on evidence", initial.current, cfg.MaxPacketSize)
	}
	if initial.current > 1024 {
		t.Errorf("a fresh connection starts at %d, above the 1024 this library used before "+
			"discovery existed; raising the ceiling must not raise what an untested path gets",
			initial.current)
	}
	if initial.current < mtuAbsoluteFloor {
		t.Errorf("a fresh connection starts at %d, below the %d floor", initial.current, mtuAbsoluteFloor)
	}
	t.Logf("ceiling %d: a fresh connection sends %d-byte datagrams (%d bytes of payload)",
		cfg.MaxPacketSize, initial.current, initial.payloadSize())

	// A path that answers nothing never grows.
	stuck := newMtuSearch(uint32(cfg.MaxPacketSize), time.Now())
	start := stuck.current
	for i := 0; i < 8; i++ {
		if !stuck.eligibleProbe(stuck.current, true) {
			break
		}
		stuck.beginProbe(uint16(i), stuck.current)
		stuck.onProbeLost(time.Now())
		if stuck.current > start {
			t.Fatalf("size grew to %d from %d while every probe was lost", stuck.current, start)
		}
	}
	if stuck.current > mtuAbsoluteFloor+mtuSearchDoneThreshold {
		t.Errorf("a path that answered nothing settled on %d; it should fall back to the floor",
			stuck.current)
	}
	t.Logf("a path that drops every probe settles on %d", stuck.current)
}
