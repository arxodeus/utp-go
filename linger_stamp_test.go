package utp

import (
	"context"
	"testing"
	"time"
)

// An acknowledgement the socket resends for a connection that has gone is
// stamped as it is sent: the current time, and the delay of the packet it
// answers.
func TestALingeringAcknowledgementIsStampedAsItIsSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	const stale, staleDiff = 1, 7
	for _, fresh := range []bool{false, true} {
		// A FIN retransmitted after ours was acknowledged, and one that
		// completes the stream.
		finSeq := uint16(500)
		ackNum := finSeq
		if fresh {
			ackNum = finSeq - 1
		}
		peerStamp := nowMicro() - 40_000 // the peer's clock, 40ms behind ours
		fin := newPacketBuilder(stFin, 4801, peerStamp, 1<<20, finSeq).WithAckNum(900).Build()
		stored := newPacketBuilder(stState, 4800, stale, 1<<20, 901).WithTimestampDiffMicros(staleDiff).WithAckNum(ackNum).Build()
		key := cidFromPacket(fin, conn.peer, cidTypes[0]).Hash()
		sock.lingerAcks.put(key, stored)

		if !sock.reAckLingering(fin, conn.peer) {
			t.Fatalf("fresh=%v: the FIN was not answered from the linger state", fresh)
		}
		var sent *packet
		deadline := time.Now().Add(2 * time.Second)
		for sent == nil && time.Now().Before(deadline) {
			for _, raw := range conn.takeEmitted() {
				if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stState {
					sent = p
				}
			}
			time.Sleep(time.Millisecond)
		}
		if sent == nil {
			t.Fatalf("fresh=%v: no acknowledgement went out", fresh)
		}
		if sent.Header.Timestamp == stale || sent.Header.TimestampDiff == staleDiff {
			t.Fatalf("fresh=%v: sent with the stamps it was stored with (timestamp %d, diff %d)",
				fresh, sent.Header.Timestamp, sent.Header.TimestampDiff)
		}
		if d := time.Duration(sent.Header.TimestampDiff) * time.Microsecond; d < 40*time.Millisecond || d > 2*time.Second {
			t.Fatalf("fresh=%v: the echoed delay is %v, want the FIN's own, 40ms and up", fresh, d)
		}
		sock.lingerAcks.remove(key)
	}
}
