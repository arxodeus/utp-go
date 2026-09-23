//go:build cgo

package utp_go

import (
	"fmt"

	"github.com/zen-eth/utp-go/native/libutp"
)

// The M2 conformance harness.
//
// Two implementations are driven with identical packet sequences and every
// emitted packet is compared field by field. libutp is driven through the
// deterministic driver in native/libutp: virtual clock, scripted random
// source, packets in and out by explicit call. This side is driven through a
// scripted transport that does the same for our socket.
//
// Both sequence numbers and connection ids are pinned, so the comparison is
// byte-for-byte on everything except the fields listed in allowedToDiffer.

// --- comparison -------------------------------------------------------------

// fieldDiff is one field on which the two implementations disagreed.
type fieldDiff struct {
	Field  string
	Ours   any
	Libutp any
}

func (d fieldDiff) String() string {
	return fmt.Sprintf("%s: ours=%v libutp=%v", d.Field, d.Ours, d.Libutp)
}

// allowedToDiffer names the fields the corpus does not require to match, and
// why. Everything else must be identical.
//
// The brief's rule is that a deliberate divergence is asserted explicitly
// rather than hidden in a tolerance, so these are named here rather than
// quietly skipped, and the corpus reports when one actually differs.
// pinnedClockConfig returns a connection config whose wall clock is the
// libutp driver's virtual one.
//
// This is what removed "Timestamp" and "TimestampDiff" from allowedToDiffer.
// They were tolerated because libutp's driver reads a virtual clock where
// this library read the real one, so the two could never agree whatever the
// implementations did -- which meant the corpus compared every field of a
// packet except the two that carry its timing.
//
// ConnectionConfig.NowMicros governs only what is stamped and measured, not
// scheduling, so a connection given the driver's clock still retransmits on
// real timers. That is enough for these two fields and not enough for ack
// *latency*, which is still uncompared; see CONFORMANCE.md.
func pinnedClockConfig(drv *libutp.Driver) *ConnectionConfig {
	cfg := NewConnectionConfig()
	cfg.NowMicros = func() uint32 { return uint32(drv.Now()) }
	return cfg
}

var allowedToDiffer = map[string]string{
	"WndSize": "the advertised receive window is a local buffer-size choice, " +
		"not a protocol requirement; libutp advertises what its read buffer has " +
		"free and we advertise ours",
}

// comparePackets diffs two decoded packets field by field.
func comparePackets(ours, theirs *packet) []fieldDiff {
	var diffs []fieldDiff
	add := func(field string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			diffs = append(diffs, fieldDiff{Field: field, Ours: a, Libutp: b})
		}
	}

	add("PacketType", ours.Header.PacketType.String(), theirs.Header.PacketType.String())
	add("Version", ours.Header.Version, theirs.Header.Version)
	add("Extension", ours.Header.Extension, theirs.Header.Extension)
	add("ConnectionId", ours.Header.ConnectionId, theirs.Header.ConnectionId)
	add("SeqNum", ours.Header.SeqNum, theirs.Header.SeqNum)
	add("AckNum", ours.Header.AckNum, theirs.Header.AckNum)
	add("WndSize", ours.Header.WndSize, theirs.Header.WndSize)
	add("Timestamp", ours.Header.Timestamp, theirs.Header.Timestamp)
	add("TimestampDiff", ours.Header.TimestampDiff, theirs.Header.TimestampDiff)
	add("BodyLen", len(ours.Body), len(theirs.Body))
	add("Body", fmt.Sprintf("%x", ours.Body), fmt.Sprintf("%x", theirs.Body))

	oursAck, theirsAck := "none", "none"
	if ours.Eack != nil {
		oursAck = fmt.Sprintf("%x", ours.Eack.Encode())
	}
	if theirs.Eack != nil {
		theirsAck = fmt.Sprintf("%x", theirs.Eack.Encode())
	}
	add("SelectiveAck", oursAck, theirsAck)

	return diffs
}

// significant splits a diff list into the differences that matter and the ones
// allowedToDiffer explains.
func significant(diffs []fieldDiff) (bad, allowed []fieldDiff) {
	for _, d := range diffs {
		if _, ok := allowedToDiffer[d.Field]; ok {
			allowed = append(allowed, d)
		} else {
			bad = append(bad, d)
		}
	}
	return bad, allowed
}
