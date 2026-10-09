package utp_go

import (
	"context"
	"testing"
	"time"
)

// dataSent is one of our data packets as it went out.
type dataSent struct {
	seq uint16
	ts  uint32
}

func takeData(t *testing.T, conn *scriptedConn) []dataSent {
	t.Helper()
	var out []dataSent
	for _, raw := range conn.takeEmitted() {
		p, err := DecodePacket(raw)
		if err != nil {
			t.Fatalf("undecodable packet: %v", err)
		}
		if p.Header.PacketType == st_data {
			out = append(out, dataSent{p.Header.SeqNum, uint32(p.Header.Timestamp)})
		}
	}
	return out
}

// peerAck is a STATE from the scripted peer acknowledging ackNum, with the
// given selective ack, echoing echo as the timestamp of the packet that drew
// it: the receiver reports the difference between its clock and that
// timestamp, and the sender subtracts it from the STATE's own.
func peerAck(ackNum uint16, sack *SelectiveAck, echo uint32) []byte {
	const peerClock = 500_000_000
	b := NewPacketBuilder(st_state, 6000+1, peerClock, 1<<20, 900+2).
		WithAckNum(ackNum).WithTsDiffMicros(peerClock - echo)
	if sack != nil {
		b = b.WithSelectiveAck(sack)
	}
	return b.Build().Encode()
}

// A cut for a packet taken for lost is undone when the acknowledgement shows
// the original arrived after all, and stands when it shows the resend did.
//
// On a link that reorders, a packet displaced far enough that three later
// ones are selectively acknowledged first is taken for lost, resent, and the
// window halved, as libutp does. The original then arrives. Under LEDBAT++
// on the reordering benchmark link, how often that happened decided which of
// two modes a run fell in, 2.35 or 2.62 Mbps.
func TestSpuriousLossCutIsUndone(t *testing.T) {
	for _, c := range []struct {
		name        string
		echoOrginal bool
		wantUndone  bool
	}{
		{"original arrived", true, true},
		{"resend arrived", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			stream, conn, clk, done := liveAccepted(t)
			defer done()
			ctrl := stream.conn.state.SentPackets.congestionCtrl
			conn.takeEmitted()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			go stream.Write(ctx, make([]byte, 256*1024))
			// The writer is not a participant of the clock: wait for what it
			// queues to go out.
			var first []dataSent
			for len(first) == 0 && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
				clk.AwaitQuiet()
				first = takeData(t, conn)
			}

			// Acknowledge whole flights until the window has grown to more
			// than four packets in flight.
			var flight []dataSent
			for round := 0; ; round++ {
				if round == 0 {
					flight = first
				} else {
					flight = takeData(t, conn)
				}
				if len(flight) >= 6 || len(flight) == 0 || round == 10 {
					break
				}
				clk.Advance(10 * time.Millisecond)
				last := flight[len(flight)-1]
				clk.AwaitReactionTo(func() { conn.inject(peerAck(last.seq, nil, last.ts)) })
				clk.AwaitQuiet()
			}
			if len(flight) < 6 {
				t.Fatalf("the window never grew past %d packets", len(flight))
			}

			// The first of the flight is late: the next three are selectively
			// acknowledged, and it is taken for lost.
			clk.Advance(10 * time.Millisecond)
			lost := flight[0]
			before := ctrl.Stats().MaxWindowSizeBytes
			sack := NewSelectiveAck([]bool{true, true, true})
			clk.AwaitReactionTo(func() { conn.inject(peerAck(lost.seq-1, sack, flight[3].ts)) })
			clk.AwaitQuiet()
			cut := ctrl.Stats().MaxWindowSizeBytes
			var resend *dataSent
			for _, d := range takeData(t, conn) {
				if d.seq == lost.seq {
					d := d
					resend = &d
				}
			}
			if cut >= before || resend == nil {
				t.Fatalf("no loss: window %d -> %d, resent %v", before, cut, resend != nil)
			}
			if resend.ts == lost.ts {
				t.Fatal("the resend has the original's timestamp; the test cannot tell them apart")
			}

			// Now the hole is filled, by one copy or the other.
			echo := resend.ts
			if c.echoOrginal {
				echo = lost.ts
			}
			clk.Advance(10 * time.Millisecond)
			clk.AwaitReactionTo(func() { conn.inject(peerAck(flight[3].seq, nil, echo)) })
			clk.AwaitQuiet()
			after := ctrl.Stats().MaxWindowSizeBytes
			undone := ctrl.Stats().SpuriousLossesUndone == 1
			t.Logf("window %d before the loss, %d after it, %d once the hole filled", before, cut, after)
			if undone != c.wantUndone || (c.wantUndone && after < before) || (!c.wantUndone && after >= before) {
				t.Fatalf("window %d before, %d cut, %d after the acknowledgement (undone: %v); expected undone %v",
					before, cut, after, undone, c.wantUndone)
			}
		})
	}
}
