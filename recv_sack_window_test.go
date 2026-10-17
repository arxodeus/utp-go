package utp

import (
	"math/rand"
	"runtime"
	"testing"
)

// The selective ack visits only its window of the reorder buffer, and must
// say exactly what a scan of everything held says -- including where the
// window wraps past 65535.
func TestSelectiveAckMatchesAFullScan(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		init := uint16(rng.Intn(65536))
		if seed%4 == 0 {
			init = uint16(65535 - rng.Intn(40)) // a window that wraps
		}
		rb := newReceiveBuffer(1<<20, init)
		held := map[uint16]bool{}
		for i := 0; i < rng.Intn(80); i++ {
			seq := init + 2 + uint16(rng.Intn(60))
			if rb.Write([]byte{1}, seq) == nil {
				held[seq] = true
			}
		}
		sack := rb.SelectiveAck()
		if len(held) == 0 {
			if sack != nil {
				t.Fatalf("seed %d: a selective ack with nothing held", seed)
			}
			continue
		}
		base := rb.AckNum() + 2
		bits := flattenSack(sack)
		for i := 0; i < selectiveAckWindow; i++ {
			if want := held[base+uint16(i)]; bits[i] != want {
				t.Fatalf("seed %d init %d: bit %d (seq %d) is %v, want %v", seed, init, i, base+uint16(i), bits[i], want)
			}
		}
	}
}

func flattenSack(s *selectiveAck) []bool {
	var out []bool
	for _, word := range s.acked {
		out = append(out, word[:]...)
	}
	return out
}

// Building one costs the same however much is held: it used to copy every
// held packet's number into a map first, on every acknowledgement.
func TestSelectiveAckCostDoesNotGrowWithTheReorderBuffer(t *testing.T) {
	rb := newReceiveBuffer(1<<20, 0)
	for seq := uint16(3); seq < 3+2000; seq += 2 {
		if err := rb.Write([]byte{1}, seq); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	const calls = 100
	for i := 0; i < calls; i++ {
		rb.SelectiveAck()
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / calls; per > 2048 {
		t.Fatalf("%d bytes allocated per selective ack with 1000 packets held, want under 2 KB", per)
	}
}
