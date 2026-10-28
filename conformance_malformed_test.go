//go:build cgo

package utp

import (
	"encoding/binary"
	"testing"
	"time"
)

// Malformed and hostile input, compared with the reference implementation.
// Accepting a packet the reference rejects would be both an incompatibility
// and an attack surface. Each case injects raw bytes into an established
// connection on both sides and requires them to agree on whether anything
// comes back.

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

// Version 1 is the only version.
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

// A selective ack whose length is not a whole number of 4-byte words. This
// is a deliberate difference: the reference implementation does not check
// the length and interprets the truncated bitfield, while this library
// rejects it, since interpreting it means guessing which acks the peer
// meant. Selective acks are always written as exactly 4 bytes, so nothing
// depends on the leniency. Being stricter than the reference risks
// interoperability; being more permissive is an attack surface. The test
// fixes the direction.
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

// unknownExtensionPacket is a valid ST_DATA whose *first* extension is of an
// unknown type, well formed and with a payload behind it.
func unknownExtensionPacket() []byte {
	raw := dataHeader()
	raw[1] = 99 // unknown extension type
	raw = append(raw, 0 /*next: none*/, 4, 0xde, 0xad, 0xbe, 0xef)
	return append(raw, []byte("payload")...)
}

// An unknown first extension type. BEP 29 says unknown extensions are
// skipped by their length, but both implementations drop the packet: a first
// extension type of 3 or more makes the reference implementation read the
// packet's version as 0, so it is not treated as uTP.
//
// The control, the same packet without the extension, is acknowledged, so
// the silence is the extension being rejected and not the connection having
// stopped answering for some other reason.
func TestMalformedUnknownExtensionType(t *testing.T) {
	plain := dataHeader()
	plain = append(plain, []byte("payload")...)
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name:           "an unknown first extension type, well formed",
			injectRaw:      unknownExtensionPacket(),
			wantNoEmission: true,
		},
		{
			name:      "the same packet without the extension is acked",
			injectRaw: plain,
		},
	})
}

// The same unknown type, second in the chain rather than first. Only the
// first extension byte is checked in the header; an unknown type reached
// through the chain is skipped by its length, as BEP 29 says. This separates
// "rejects unknown extensions" from "rejects an unknown extension byte in
// the header".
func TestMalformedUnknownExtensionTypeInChain(t *testing.T) {
	raw := dataHeader()
	raw[1] = 1 // first extension: selective ack, which is known
	raw = append(raw, 99 /*next: the unknown type*/, 4, 0, 0, 0, 0)
	raw = append(raw, 0 /*next: none*/, 4, 0xde, 0xad, 0xbe, 0xef)
	raw = append(raw, []byte("payload")...)
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "an unknown extension type behind a known one", injectRaw: raw},
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
	return runDivergence(t, raws, false)
}

// runDivergenceStepsAnswered is runDivergenceSteps for a caller that expects
// an answer to the last packet: it waits for one rather than for quiet.
func runDivergenceStepsAnswered(t *testing.T, raws [][]byte) (ours, libutpOut [][]byte) {
	t.Helper()
	return runDivergence(t, raws, true)
}

func runDivergence(t *testing.T, raws [][]byte, answered bool) (ours, libutpOut [][]byte) {
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
	// settle declares a connection done after 60 ms with nothing new, which
	// on a loaded machine can come before its answer to the last packet. A
	// caller that expects an answer waits for one.
	for deadline := time.Now().Add(5 * time.Second); answered && conn.emittedCount() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	ours = conn.takeEmitted()

	return ours, libutpOut
}
