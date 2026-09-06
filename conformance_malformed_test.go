//go:build cgo

package utp_go

import (
	"encoding/binary"
	"testing"
)

// Malformed and hostile input, compared against libutp.
//
// The rule from the brief: every validation that causes libutp to drop or
// reset matters, because if this implementation accepts a packet libutp
// rejects, that is both an incompatibility and an attack surface.
//
// Each case injects raw bytes into an established connection on both sides
// and requires them to agree on whether anything comes back.

// wireHeader builds a well-formed 20-byte header, which each case then
// corrupts in one specific way.
func wireHeader(pktType byte, version byte, extension byte, connID uint16, seq, ack uint16) []byte {
	b := make([]byte, 20)
	b[0] = pktType<<4 | version
	b[1] = extension
	binary.BigEndian.PutUint16(b[2:4], connID)
	binary.BigEndian.PutUint32(b[4:8], 200000)    // timestamp
	binary.BigEndian.PutUint32(b[8:12], 0)        // timestamp diff
	binary.BigEndian.PutUint32(b[12:16], 1048576) // window
	binary.BigEndian.PutUint16(b[16:18], seq)
	binary.BigEndian.PutUint16(b[18:20], ack)
	return b
}

// dataHeader is a valid ST_DATA header for the established corpus connection.
func dataHeader() []byte {
	return wireHeader(0 /*ST_DATA*/, 1, 0, corpusSynConnID+1, corpusSynSeq+1, corpusPinnedSeq-1)
}

func malformedCase(t *testing.T, name string, raw []byte) {
	t.Helper()
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: name, injectRaw: raw, wantNoEmission: true},
	})
}

func TestMalformedEmptyPacket(t *testing.T) {
	malformedCase(t, "a zero-length datagram", []byte{})
}

func TestMalformedTruncatedHeader(t *testing.T) {
	for _, n := range []int{1, 4, 12, 19} {
		t.Run(nameForLen(n), func(t *testing.T) {
			malformedCase(t, "a header cut short", dataHeader()[:n])
		})
	}
}

func nameForLen(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10)) + "bytes"
}

// uTP version 1 is the only version either implementation speaks.
func TestMalformedWrongVersion(t *testing.T) {
	for _, v := range []byte{0, 2, 15} {
		t.Run(string(rune('0'+v%10))+"-version", func(t *testing.T) {
			raw := dataHeader()
			raw[0] = 0<<4 | v
			malformedCase(t, "a version nibble that is not 1", raw)
		})
	}
}

// Types beyond ST_SYN (4) are undefined.
func TestMalformedUnknownPacketType(t *testing.T) {
	for _, pt := range []byte{5, 9, 15} {
		t.Run(string(rune('0'+pt%10))+"-type", func(t *testing.T) {
			raw := dataHeader()
			raw[0] = pt<<4 | 1
			malformedCase(t, "an undefined packet type", raw)
		})
	}
}

// The extension byte says an extension follows, but the packet ends.
func TestMalformedExtensionPromisedButAbsent(t *testing.T) {
	raw := dataHeader()
	raw[1] = 1 // selective ack follows
	malformedCase(t, "an extension promised but not present", raw)
}

// An extension header whose length runs off the end of the packet.
func TestMalformedExtensionLengthOverrunsPacket(t *testing.T) {
	raw := append(dataHeader(), 0 /*next*/, 200 /*len*/, 1, 2, 3, 4)
	raw[1] = 1
	malformedCase(t, "an extension length past the end of the packet", raw)
}

// A selective ack whose length is not a whole number of 4-byte words.
//
// This is a deliberate divergence, and the only one in this file. libutp
// performs no length validation on a selective-ack extension at all -- its
// parser records the pointer and moves on (utp_internal.cpp:1844, `case 1`),
// with only the generic "does the length fit in the packet" check applied. It
// therefore accepts a 1, 3, 5 or 7-byte bitfield and interprets it.
//
// We reject it. A bitfield that is not a whole number of words is truncated,
// and interpreting it means guessing which acks the peer meant. libutp never
// emits such an extension -- it always writes exactly 4 bytes
// (utp_internal.cpp:815-818) -- so nothing in the wild depends on the
// leniency, and being stricter here cannot make us unroutable.
//
// The direction matters: being stricter than the reference is an
// interoperability risk, being more permissive is an attack surface. This
// test pins the direction, so a future change that makes us accept these
// would fail.
func TestMalformedSelectiveAckLength(t *testing.T) {
	for _, extLen := range []byte{1, 3, 5, 7} {
		t.Run(string(rune('0'+extLen))+"-len", func(t *testing.T) {
			body := make([]byte, int(extLen))
			raw := append(dataHeader(), append([]byte{0, extLen}, body...)...)
			raw[1] = 1
			ours, libutpOut := runDivergenceCase(t, raw)
			if len(ours) != 0 {
				t.Errorf("we emitted %d packets for a %d-byte selective ack; expected to reject it", len(ours), extLen)
			}
			if len(libutpOut) == 0 {
				t.Errorf("libutp emitted nothing for a %d-byte selective ack; this case no longer "+
					"documents a divergence and should be re-examined", extLen)
			}
			t.Logf("deliberate divergence: a %d-byte selective ack -- we drop it, libutp accepts it "+
				"and emits %d packet(s)", extLen, len(libutpOut))
		})
	}
}

// A zero-length selective ack, where both agree.
func TestMalformedZeroLengthSelectiveAck(t *testing.T) {
	raw := append(dataHeader(), 0, 0)
	raw[1] = 1
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "a zero-length selective ack", injectRaw: raw},
	})
}

// A chain of extensions that never terminates within the packet.
func TestMalformedExtensionChainLoops(t *testing.T) {
	// Each extension's "next" points at another extension, forever.
	raw := dataHeader()
	raw[1] = 1
	for i := 0; i < 8; i++ {
		raw = append(raw, 1 /*next: another extension*/, 4, 0, 0, 0, 0)
	}
	malformedCase(t, "an extension chain that never terminates", raw)
}

// An unknown extension type. BEP 29 says unknown extensions are skipped using
// their length, so a well-formed one should be tolerated by both.
func TestMalformedUnknownExtensionType(t *testing.T) {
	raw := dataHeader()
	raw[1] = 99 // unknown extension type
	raw = append(raw, 0 /*next: none*/, 4, 0xde, 0xad, 0xbe, 0xef)
	raw = append(raw, []byte("payload")...)
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "an unknown extension type, well formed", injectRaw: raw},
	})
}

// A packet for a connection id neither side knows. Both answer with a RESET,
// so this compares rather than expecting silence.
func TestMalformedUnknownConnectionId(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "a packet for an unknown connection", injectRaw: wireHeader(0, 1, 0, 44444, 1, 1)},
	})
}

// runDivergenceCase drives both implementations to an established connection,
// injects one raw packet, and returns what each emitted, without asserting
// that they agree. It is for cases where they deliberately do not.
func runDivergenceCase(t *testing.T, raw []byte) (ours, libutpOut [][]byte) {
	t.Helper()
	return runDivergenceSteps(t, [][]byte{raw})
}

// runDivergenceSteps drives both implementations through a handshake and then
// the given raw packets, and returns what each emitted in response to the
// last one, without asserting that they agree. It is for cases where they
// deliberately do not.
func runDivergenceSteps(t *testing.T, raws [][]byte) (ours, libutpOut [][]byte) {
	t.Helper()

	drv, err := libutpNewDriverForCorpus()
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	drv.Listen()
	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.IssueAcks()
	for _, raw := range raws {
		drv.ClearEmitted()
		drv.Inject(raw)
		drv.IssueAcks()
	}
	libutpOut = drv.Emitted()

	restore := pinRandom(corpusPinnedSeq)
	defer restore()
	conn, sock, cancel := goResponderForCorpus(t)
	defer cancel()
	defer sock.Close()
	conn.inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	conn.settle()
	for _, raw := range raws {
		conn.takeEmitted()
		conn.inject(raw)
		conn.settle()
	}
	ours = conn.takeEmitted()

	return ours, libutpOut
}
