//go:build cgo

package utp_go

import (
	"fmt"
	"testing"
	"time"
)

// The delay classic LEDBAT acts on is not the one the latest acknowledgement
// reported. libutp takes the least of the last three queueing-delay samples
// (DelayHist::get_value, utp_internal.cpp:383-391, CUR_DELAY_SIZE 3 at :44),
// each measured against the base as it stood when it arrived (:357-362), so
// one late packet does not move the window and three in a row do.
//
// libutp is driven alone through acknowledgements whose delays jump about,
// and its congestion-control log gives both the delay each one reported
// (actual_delay) and the delay apply_ccontrol used (our_delay). The same
// reported delays go into our controller, and after each the delay it would
// use has to be libutp's.
//
// Every acknowledgement covers one packet and reports a whole number of
// milliseconds over a fixed base, the round trip is longer than any of the
// delays (the window is full, so the queue of unacknowledged packets is
// long), and so libutp's clamp to the round trip (:1621) never decides the
// value and the log's millisecond resolution loses nothing.
func TestConformanceDelayFilter(t *testing.T) {
	// Spikes of one, two and three acknowledgements, a fall, and a rise.
	pattern := []uint32{0, 0, 0, 5, 5, 60, 5, 5, 60, 60, 5, 5, 60, 60, 60, 60, 5, 20, 20, 20, 40, 10, 40, 40, 40, 0, 80, 80, 80, 3}
	var phases []ccPhase
	for i, d := range pattern {
		phases = append(phases, ccPhase{name: fmt.Sprintf("ack %d", i), acks: 1, delayMs: d, gap: 20 * time.Millisecond})
	}
	entries := libutpCCTrace(t, 4<<20, phases)
	if len(entries) != len(pattern) {
		t.Fatalf("libutp logged %d congestion-control updates for %d acknowledgements", len(entries), len(pattern))
	}

	c := newDefaultController(fromConnConfig(NewConnectionConfig()))
	start := time.Now() // the base-delay window ages samples on the real clock
	var distinct bool   // did the filter ever differ from the latest sample?
	for i, e := range entries {
		if want := int64(20000 + pattern[i]*1000); e.actualUs != want {
			t.Fatalf("entry %d: libutp logged actual_delay %dus, the harness sent %dus: the trace "+
				"and the pattern are out of step", i, e.actualUs, want)
		}
		c.OnAckDelay(time.Duration(e.actualUs)*time.Microsecond, start.Add(time.Duration(i)*20*time.Millisecond))
		got := int64(c.filteredDelayMicros()) / 1000
		if got != e.ourDelayMs {
			t.Errorf("ack %d reporting %dms of queueing: libutp acted on %dms, ours on %dms",
				i, pattern[i], e.ourDelayMs, got)
		}
		if e.ourDelayMs != int64(pattern[i]) {
			distinct = true
		}
	}
	if !distinct {
		t.Fatal("libutp acted on every acknowledgement's own delay; the pattern does not exercise a filter")
	}
}
