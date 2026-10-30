package utp

import (
	"fmt"
)

// The conformance harness.
//
// Two implementations are driven with identical packet sequences and every
// emitted packet is compared field by field. The reference implementation is
// driven through its deterministic driver (refDriver): virtual clock,
// scripted random source, packets in and out by explicit call. This side is
// driven through a scripted transport that does the same for our socket.
//
// Sequence numbers and connection ids are pinned on both sides, so the
// comparison is byte for byte except for the fields named in allowedToDiffer.

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

// pinnedClockConfig returns a connection config whose wall clock is the
// driver's virtual clock, so the timestamp fields can be compared.
// nowMicros governs only what is stamped and measured, not scheduling, so
// such a connection still retransmits on real timers.
func pinnedClockConfig(drv *refDriver) *ConnectionConfig {
	cfg := NewConnectionConfig()
	cfg.nowMicros = func() uint32 { return uint32(drv.Now()) }
	return cfg
}

// allowedToDiffer names the fields the corpus does not require to match, and
// why. Every other field must be identical, and the corpus reports when a
// listed field actually differs.
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
	add("ConnectionID", ours.Header.ConnectionID, theirs.Header.ConnectionID)
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
