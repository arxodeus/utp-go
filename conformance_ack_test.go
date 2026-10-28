//go:build cgo

package utp

import (
	"fmt"
	"testing"
	"time"
)

// TestConformanceAckCoalescing compares how many STATE packets each
// implementation emits for a batch of ST_DATA packets delivered back to back.
// The reference implementation defers acknowledgements and flushes them once
// per read batch, so a batch costs exactly one STATE packet; that is checked
// on every run rather than assumed. This library does the same: the socket's
// reader sends the acknowledgement once nothing more is waiting
// (Socket.readLoop, QueuedReader).
func TestConformanceAckCoalescing(t *testing.T) {
	for _, batch := range []int{1, 2, 4, 8, 16} {
		t.Run(fmt.Sprintf("%d-packets", batch), func(t *testing.T) {
			theirs := libutpAcksForBatch(t, batch)
			ours := ourAcksForBatch(t, batch)
			t.Logf("%d data packets in one batch: libutp emitted %d ack(s), we emitted %d",
				batch, theirs, ours)

			if theirs != 1 {
				t.Errorf("libutp emitted %d acks for a batch of %d, want 1; "+
					"the reference behaviour this test pins has changed", theirs, batch)
			}
			if ours != theirs {
				t.Errorf("we emitted %d acks for a batch of %d, libutp %d", ours, batch, theirs)
			}
		})
	}
}

// libutpAcksForBatch drives the reference responder through the handshake,
// injects batch ST_DATA packets, and returns the number of packets it emits
// when deferred acks are flushed.
func libutpAcksForBatch(t *testing.T, batch int) int {
	t.Helper()
	drv, err := libutpNewDriverForCorpus()
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()

	drv.Listen()
	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.IssueAcks()
	drv.Inject(fuzzPrimingPacket())
	drv.IssueAcks()
	drv.ClearEmitted()

	for _, pkt := range ackBatchPackets(batch) {
		drv.Inject(pkt)
	}
	drv.IssueAcks()
	return len(drv.Emitted())
}

// ourAcksForBatch drives our responder through the same sequence.
func ourAcksForBatch(t *testing.T, batch int) int {
	t.Helper()
	restore := pinRandom(corpusPinnedSeq)
	defer restore()

	conn, sock, cancel := goResponderForCorpus(t)
	defer cancel()
	defer sock.Close()

	conn.inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	conn.settle()
	conn.inject(fuzzPrimingPacket())
	conn.settle()
	conn.takeEmitted()

	// Queue the whole batch before any of it is delivered, so the test
	// measures coalescing rather than injection speed.
	conn.hold()
	for _, pkt := range ackBatchPackets(batch) {
		conn.inject(pkt)
	}
	conn.release()

	// settle returns after one quiet period; a batch may still span two
	// passes, so wait long enough that a third would have happened.
	time.Sleep(200 * time.Millisecond)
	return len(conn.takeEmitted())
}

// ackBatchPackets builds batch in-order single-byte ST_DATA packets following
// the corpus handshake, each acknowledging the responder's own SYN-ACK.
func ackBatchPackets(batch int) [][]byte {
	pkts := make([][]byte, 0, batch)
	for i := 0; i < batch; i++ {
		seq := corpusSynSeq + 2 + uint16(i)
		pkts = append(pkts, newPacketBuilder(stData, corpusSynConnID+1, 200000+uint32(i),
			corpusWindow, seq).WithAckNum(corpusPinnedSeq-1).
			WithPayload([]byte("x")).Build().Encode())
	}
	return pkts
}
