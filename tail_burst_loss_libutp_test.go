//go:build cgo

package utp_go

import (
	"testing"
	"time"
)

// libutpTailBurstRecovery is tailBurstRecovery for libutp, through the
// conformance driver on the same virtual clock: the same round trip, the same
// burst, every packet but the burst's own acknowledged a round trip after it
// left, and the burst lost but for its last packet.
func libutpTailBurstRecovery(t *testing.T, rtt time.Duration, burst int) (time.Duration, int) {
	t.Helper()
	rr := newRecoveryRun(t, 0)
	defer rr.close()
	rr.takeLibutp()
	rr.takeOurs()
	if _, err := rr.drv.Write(make([]byte, 2<<20)); err != nil {
		t.Fatal(err)
	}

	got := map[uint16]bool{}
	cum := uint16(initiatorConnSeed)
	var stray uint16
	ts := uint32(300000)
	ack := func() []byte {
		for got[cum+1] {
			cum++
		}
		var bits []bool
		if stray != 0 && wrappingLessThan(cum+1, stray) {
			bits = make([]bool, 32)
			bits[stray-cum-2] = true
		}
		return ackFor(ts, cum, bits)
	}

	var lostFrom, lostTo uint16
	for round := 0; ; round++ {
		if round > 200 {
			t.Fatalf("libutp's window never reached %d packets", burst)
		}
		var seqs []uint16
		for _, d := range rr.takeLibutp() {
			seqs = append(seqs, d.seq)
		}
		if len(seqs) >= burst {
			lostFrom, lostTo, stray = seqs[0], seqs[len(seqs)-2], seqs[len(seqs)-1]
			got[stray] = true
			break
		}
		for _, s := range seqs {
			got[s] = true
		}
		rr.advance(rtt)
		ts += uint32(rtt.Microseconds())
		rr.injectLibutp(ack())
	}

	const step = 5 * time.Millisecond
	type delivery struct {
		seq uint16
		at  time.Duration
	}
	var pending []delivery
	var elapsed time.Duration
	for wrappingLessThan(cum, lostTo) {
		if elapsed > time.Minute {
			t.Fatalf("libutp: burst %d-%d not recovered after a minute; peer holds through %d", lostFrom, lostTo, cum)
		}
		for _, d := range rr.takeLibutp() {
			pending = append(pending, delivery{d.seq, elapsed + rtt})
		}
		rr.advance(step)
		elapsed += step
		ts += uint32(step.Microseconds())
		var due []uint16
		keep := pending[:0]
		for _, p := range pending {
			if elapsed >= p.at {
				due = append(due, p.seq)
			} else {
				keep = append(keep, p)
			}
		}
		pending = keep
		if len(due) > 0 {
			for _, s := range due {
				got[s] = true
			}
			rr.injectLibutp(ack())
		}
	}
	return elapsed, int(lostTo-lostFrom) + 1
}

// The same tail burst, measured against libutp: ours recovers it no slower.
func TestTailBurstLossAgainstLibutp(t *testing.T) {
	const rtt = 50 * time.Millisecond
	theirs, theirLost := libutpTailBurstRecovery(t, rtt, 25)
	ours, ourLost := tailBurstRecovery(t, rtt, 25)
	t.Logf("tail burst: libutp lost %d and recovered in %v; ours lost %d and recovered in %v",
		theirLost, theirs, ourLost, ours)
	if ours > theirs {
		t.Fatalf("ours took %v to recover %d packets, libutp %v for %d", ours, ourLost, theirs, theirLost)
	}
}
