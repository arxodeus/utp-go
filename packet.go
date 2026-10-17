package utp

import (
	"encoding/binary"
	"errors"
	"time"
)

const (
	minimalHeaderSize                 = 20
	minimalHeaderSizeWithSelectiveAck = 26
	protocolVersionOne                = 1
	// maxKnownExtension is the highest extension type either implementation
	// recognises: 0 none, 1 selective ack, 2 extension bits.
	maxKnownExtension = 2
	zeroMoment        = time.Duration(0)
	acksArrayLength   = byte(4)
	packetHeaderLen   = 20
	selectiveAckBits  = 32
	extensionTypeLen  = 1
	extensionLenLen   = 1
)

const (
	st_data packetType = iota
	st_fin
	st_state
	st_reset
	st_syn
)

var (
	errInvalidHeaderSize           = errors.New("invalid header size")
	errUnsupportedVersion          = errors.New("unsupported protocol version")
	errUnknownExtension            = errors.New("unknown extension type")
	errInvalidPacketType           = errors.New("invalid packet type")
	errInsufficientLen             = errors.New("insufficient length for extension")
	errInsufficientSelectiveAckLen = errors.New("insufficient length for selective ACK")
	errInvalidSelectiveAckLen      = errors.New("invalid length for selective ACK")
)

type packetType byte

func (p packetType) Check() error {
	if p > 4 {
		return errInvalidPacketType
	}
	return nil
}

func (p *packetType) String() string {
	switch *p {
	case st_data:
		return "st_data"
	case st_fin:
		return "st_fin"
	case st_state:
		return "st_state"
	case st_reset:
		return "st_reset"
	case st_syn:
		return "st_syn"
	default:
		return "UNKNOWN"
	}
}

type packetHeaderV1 struct {
	PacketType    packetType
	Version       byte
	Extension     byte
	ConnectionId  uint16
	Timestamp     int64
	TimestampDiff uint32
	WndSize       uint32
	SeqNum        uint16
	AckNum        uint16
}

func (h *packetHeaderV1) encodeTypeVer() byte {
	typeVer := byte(h.PacketType)<<4 | h.Version
	return typeVer
}

func (h *packetHeaderV1) EncodeToBytes() []byte {
	return h.appendTo(make([]byte, 0, minimalHeaderSize))
}

// appendTo appends the header's 20 bytes to b.
func (h *packetHeaderV1) appendTo(b []byte) []byte {
	b = append(b, h.encodeTypeVer(), h.Extension)             // 2
	b = binary.BigEndian.AppendUint16(b, h.ConnectionId)      // 2
	b = binary.BigEndian.AppendUint32(b, uint32(h.Timestamp)) // 4
	b = binary.BigEndian.AppendUint32(b, h.TimestampDiff)     // 4
	b = binary.BigEndian.AppendUint32(b, h.WndSize)           // 4
	b = binary.BigEndian.AppendUint16(b, h.SeqNum)            // 2
	b = binary.BigEndian.AppendUint16(b, h.AckNum)            // 2
	return b
}

// IsPacket reports whether b is a uTP packet for this implementation, as far
// as its header can say: the length, the packet type, the version nibble and
// the first extension byte, the same four things libutp checks before it will
// look at a packet at all (utp_internal.cpp:2481, :2834). It is the decoder's
// own validation, so a datagram this accepts is one the socket can parse, and
// one it refuses is one the socket would drop. utpnet uses it to tell uTP from
// another protocol sharing the port.
func IsPacket(b []byte) bool {
	if len(b) < minimalHeaderSize {
		return false
	}
	var h packetHeaderV1
	return decodePacketHeaderInto(&h, b[:minimalHeaderSize]) == nil
}

func decodePacketHeader(value []byte) (*packetHeaderV1, error) {
	h := new(packetHeaderV1)
	if err := decodePacketHeaderInto(h, value); err != nil {
		return nil, err
	}
	return h, nil
}

// decodePacketHeaderInto is decodePacketHeader into a header the caller
// provides.
func decodePacketHeaderInto(h *packetHeaderV1, value []byte) error {
	if len(value) < minimalHeaderSize {
		return errInvalidHeaderSize
	}

	packetTypeVal := packetType(value[0] >> 4)
	if err := packetTypeVal.Check(); err != nil {
		return err
	}

	// Version 1 is the only version either this implementation or libutp
	// speaks. libutp drops anything else outright (utp_internal.cpp:2834),
	// and accepting what the reference rejects is both an incompatibility and
	// an attack surface: it lets a peer reach the rest of this decoder with a
	// packet no real implementation would have sent.
	version := value[0] & 0x0F
	if version != protocolVersionOne {
		return errUnsupportedVersion
	}
	versionVal := version

	// The first extension byte must name a known extension: 0 (none),
	// 1 (selective ack) or 2 (extension bits).
	//
	// libutp folds this into its version check. UTP_Version
	// (utp_internal.cpp:2481) returns the version only if
	// `pf->type() < ST_NUM_STATES && pf->ext < 3`, and returns 0 otherwise,
	// which the caller then rejects as an unsupported version
	// (utp_internal.cpp:2834). So a packet whose *first* extension is type 3
	// or above is dropped outright.
	//
	// Only the first one: an unknown extension further along a chain is
	// skipped by its length, which is what BEP 29 describes. Verified
	// empirically against libutp -- a chain of selective-ack followed by
	// extension 99 is accepted, while extension 99 alone is not.
	//
	// Accepting what the reference rejects is both an incompatibility and an
	// attack surface, so this rejects too.
	extension := value[1]
	if extension > maxKnownExtension {
		return errUnknownExtension
	}

	connID := binary.BigEndian.Uint16(value[2:4])
	tsMicros := binary.BigEndian.Uint32(value[4:8])
	tsDiffMicros := binary.BigEndian.Uint32(value[8:12])
	windowSize := binary.BigEndian.Uint32(value[12:16])
	seqNum := binary.BigEndian.Uint16(value[16:18])
	ackNum := binary.BigEndian.Uint16(value[18:20])

	*h = packetHeaderV1{
		PacketType:    packetTypeVal,
		Version:       versionVal,
		Extension:     extension,
		ConnectionId:  connID,
		Timestamp:     int64(tsMicros),
		TimestampDiff: tsDiffMicros,
		WndSize:       windowSize,
		SeqNum:        seqNum,
		AckNum:        ackNum,
	}
	return nil
}

// packetWithHeader is a packet and its header in one allocation, for the
// decoder and the builder, which make one of each for every packet.
type packetWithHeader struct {
	p packet
	h packetHeaderV1
}

// selectiveAck represents a selective acknowledgment
type selectiveAck struct {
	acked [][selectiveAckBits]bool
}

// newSelectiveAck creates a new SelectiveAck from a slice of booleans
func newSelectiveAck(acked []bool) *selectiveAck {
	chunks := len(acked) / selectiveAckBits
	remainder := len(acked) % selectiveAckBits

	ack := &selectiveAck{acked: make([][selectiveAckBits]bool, chunks)}
	for i := 0; i < chunks; i++ {
		var fragment [32]bool
		copy(fragment[:], acked[i*selectiveAckBits:(i+1)*selectiveAckBits])
		ack.acked[i] = fragment
	}

	if remainder > 0 {
		var fragment [32]bool
		copy(fragment[:], acked[chunks*selectiveAckBits:])
		ack.acked = append(ack.acked, fragment)
	}

	return ack
}

func (s *selectiveAck) EncodedLen() int {
	return (len(s.acked) * selectiveAckBits) / 8
}

func (s *selectiveAck) Acked() []bool {
	result := make([]bool, 0, len(s.acked)*selectiveAckBits)
	for _, boolArray := range s.acked {
		result = append(result, boolArray[:]...)
	}
	return result
}

// Encode renders the selective-ack bitmask.
//
// Bit order is least-significant-first within each byte: the LSB of the first
// byte represents ack_nr+2, the next bit ack_nr+3, and so on. That is what
// BEP 29 specifies and what libutp emits -- it builds the mask with
// `m |= 1 << i` for the i'th packet past ack_nr+2 and writes the low byte
// first (utp_internal.cpp:806-818). A reversed order would agree with a
// decoder reversed the same way, so only a comparison against libutp checks
// it. History: HISTORY.md, "selectiveAck.Encode".
func (s *selectiveAck) Encode() []byte {
	var bitmask []byte
	for _, word := range s.acked {
		for i := 0; i < len(word); i += 8 {
			var byteVal uint8
			for j := 0; j < 8; j++ {
				if i+j < len(word) && word[i+j] {
					byteVal |= 1 << j
				}
			}
			bitmask = append(bitmask, byteVal)
		}
	}
	return bitmask
}

// decodeSelectiveAck decodes a byte slice into a SelectiveAck
func decodeSelectiveAck(data []byte) (*selectiveAck, error) {
	if len(data) < 4 {
		return nil, errInsufficientSelectiveAckLen
	}
	if len(data)%4 != 0 {
		return nil, errInvalidSelectiveAckLen
	}

	acked := make([][selectiveAckBits]bool, len(data)/4)
	for i := 0; i < len(data); i += 4 {
		var tmp [selectiveAckBits]bool
		for j := 0; j < 4; j++ {
			byteVal := data[i+j]
			for k := 0; k < 8; k++ {
				// Least-significant bit first, matching Encode and BEP 29.
				tmp[j*8+k] = (byteVal & (1 << k)) != 0
			}
		}
		acked[i/4] = tmp
	}

	return &selectiveAck{acked: acked}, nil
}

type packet struct {
	Header *packetHeaderV1
	Eack   *selectiveAck
	Body   []byte
	// chain is the extension chain as it arrived, in order, for a decoded
	// packet, and nil for one built here. See wireExtensions.
	chain []extensionData
	// borrowed says Body is a slice of the socket reader's buffer, which the
	// next read reuses. Whatever keeps the packet beyond the call that
	// received it takes the body first. See own.
	borrowed bool
}

// own gives the packet a body of its own, if it was borrowed.
//
// The socket decodes each datagram from its read buffer and processes most
// of them before it reads again -- an established connection's, inline -- so
// most need no copy: in-order data goes straight from the read buffer into
// the receive buffer, as libutp hands its application a pointer into the
// packet it received (utp_call_on_read, utp_internal.cpp:2351). Only a packet
// kept for later -- queued for a connection's loop, or a SYN waiting for an
// Accept -- takes its body, here. The receive buffer copies what it holds out
// of order (receiveBuffer.Write).
func (p *packet) own() {
	if p.borrowed {
		if len(p.Body) > 0 {
			p.Body = append([]byte(nil), p.Body...)
		}
		p.borrowed = false
	}
}

func (p *packet) EncodedLen() int {
	length := minimalHeaderSize
	for _, e := range p.wireExtensions() {
		length += extensionTypeLen + extensionLenLen + len(e.payload)
	}
	length += len(p.Body)
	return length
}

// extensionBits is the "extension bits" extension's type (BEP 29).
const extensionBits = 2

// malformedExtensionBits reports an extension-bits extension of any length
// but 8, which libutp refuses (utp_internal.cpp:1850-1856).
func (p *packet) malformedExtensionBits() bool {
	for _, e := range p.chain {
		if e.extension == extensionBits && len(e.payload) != 8 {
			return true
		}
	}
	return false
}

// wireExtensions is the extension chain Encode writes, in order.
//
// A packet built here carries at most a selective ack. A decoded packet
// carries every extension it arrived with, in the order it arrived, so that
// decoding and encoding again gives back the same bytes: unknown types
// further along the chain, which both this decoder and libutp step over
// (utp_internal.cpp:1844-1866), and extension 2, whose contents nothing here
// reads.
//
// The selective ack keeps its place in the chain, and its bytes come from
// Eack, so a caller that changes Eack changes what is written. Its place is
// the last type-1 extension, because that is the one the decoder reads, as
// libutp's loop does. If Eack is nil that entry is dropped -- unless it was
// empty, in which case it never became an Eack and is kept as it came.
//
// An unknown type is never written first. A packet whose first extension is
// unknown is rejected by this decoder and by libutp's version check
// (utp_internal.cpp:2480), so leading with one would make the packet
// unreadable. It can only happen if a caller cleared the selective ack that
// came first, and then the unknown extensions ahead of the first known one
// are dropped. History: HISTORY.md, "packet.wireExtensions".
func (p *packet) wireExtensions() []extensionData {
	if len(p.chain) == 0 {
		if p.Eack != nil {
			return []extensionData{{extension: 1, payload: p.Eack.Encode()}}
		}
		return nil
	}
	slot := -1
	for i, e := range p.chain {
		if e.extension == 1 {
			slot = i
		}
	}
	out := make([]extensionData, 0, len(p.chain)+1)
	if slot < 0 && p.Eack != nil {
		out = append(out, extensionData{extension: 1, payload: p.Eack.Encode()})
	}
	for i, e := range p.chain {
		if i == slot {
			switch {
			case p.Eack != nil:
				e = extensionData{extension: 1, payload: p.Eack.Encode()}
			case len(e.payload) != 0:
				continue
			}
		}
		out = append(out, e)
	}
	for len(out) > 0 && out[0].extension > maxKnownExtension {
		out = out[1:]
	}
	return out
}

// Encode writes the packet to the wire.
//
// The header's extension byte is derived from what is actually being written
// rather than taken from p.Header, so that everything this produces can be
// decoded again: a decoded packet keeps whatever extension byte the peer
// sent, while Eack is nil unless a selective ack was retained.
// FuzzDecodePacket holds it to that; history: HISTORY.md,
// "packet.Encode".
func (p *packet) Encode() []byte {
	return p.appendEncoded(make([]byte, 0, p.EncodedLen()))
}

// appendEncoded appends the packet's wire form to bytes, so that the send
// path can encode into a buffer it reuses.
func (p *packet) appendEncoded(bytes []byte) []byte {

	exts := p.wireExtensions()
	header := *p.Header
	header.Extension = 0
	if len(exts) > 0 {
		header.Extension = exts[0].extension
	}
	bytes = header.appendTo(bytes)

	// Each extension is introduced by the one before it: the header names
	// the first, and each carries the type of the next.
	for i, e := range exts {
		var next byte
		if i+1 < len(exts) {
			next = exts[i+1].extension
		}
		bytes = append(bytes, next, byte(len(e.payload)))
		bytes = append(bytes, e.payload...)
	}

	// Append payload
	bytes = append(bytes, p.Body...)

	return bytes
}

// decodePacket decodes one datagram. If borrowed is set, b is the reader's
// buffer, reused once the datagram has been dispatched: the packet's body
// still refers to it, and is taken by own when the packet is kept. Nothing
// else in the packet refers to b: the header is decoded into fields and each
// extension copied.
func decodePacket(b []byte, borrowed bool) (*packet, error) {
	receivedBytesLength := len(b)
	if receivedBytesLength < minimalHeaderSize {
		return nil, errInvalidHeaderSize
	}

	ph := &packetWithHeader{}
	p := &ph.p
	header := &ph.h
	if err := decodePacketHeaderInto(header, b[:minimalHeaderSize]); err != nil {
		return nil, err
	}

	extensions, extensionsLen, err := decodeRawExtensions(header.Extension, b[minimalHeaderSize:])
	if err != nil {
		return nil, err
	}

	var extension extensionData
	for _, extensionData := range extensions {
		if extensionData.extension == 1 {
			extension = extensionData
		}
	}
	var ack *selectiveAck
	if len(extension.payload) != 0 {
		if ack, err = decodeSelectiveAck(extension.payload); err != nil {
			return nil, err
		}
	}

	payloadStartIndex := minimalHeaderSize + extensionsLen
	var payload []byte
	if len(b) == payloadStartIndex {
		payload = make([]byte, 0)
	} else {
		payload = b[payloadStartIndex:]
	}
	// A zero-length ST_DATA is accepted, not rejected.
	//
	// libutp guards only the delivery to the application with `count > 0`
	// and advances ack_nr unconditionally (utp_internal.cpp:2342-2355), so it
	// acks such a packet like any other. Rejecting it here dropped the packet
	// before the connection ever saw it, which meant it was never acked and
	// the peer retransmitted it forever -- a silent stall against any peer
	// that sends one.
	// Every extension is kept, in order (see wireExtensions), so the header's
	// extension byte stays as it arrived, and it agrees with what Encode will
	// write.
	p.Header = header
	p.Eack = ack
	p.Body = payload
	p.chain = extensions
	p.borrowed = borrowed
	return p, nil
}

type extensionData struct {
	extension byte
	payload   []byte
}

func decodeRawExtensions(firstExt byte, data []byte) ([]extensionData, int, error) {
	ext := firstExt
	index := 0
	extensions := make([]extensionData, 0)

	for ext != 0 {
		// Check if enough data for header (2 bytes)
		if len(data[index:]) < 2 {
			return nil, 0, errInsufficientLen
		}

		// Read next extension type
		nextExt := data[index]

		// Read extension length
		extLen := int(data[index+1])

		// Calculate start of extension data
		extStart := index + 2

		// Check if enough data for extension content
		if len(data[extStart:]) < extLen {
			return nil, 0, errInsufficientLen
		}

		// Copy extension data
		extData := make([]byte, extLen)
		copy(extData, data[extStart:extStart+extLen])

		// Add to extensions list
		extensions = append(extensions, extensionData{
			extension: ext,
			payload:   extData,
		})

		// Move to next extension
		ext = nextExt
		index = extStart + extLen
	}

	return extensions, index, nil
}

type packetBuilder struct {
	packetType   packetType
	connID       uint16
	tsMicros     uint32
	tsDiffMicros uint32
	windowSize   uint32
	seqNum       uint16
	ackNum       uint16
	selectiveAck *selectiveAck
	payload      []byte
}

func newPacketBuilder(
	packetType packetType,
	connID uint16,
	tsMicros uint32,
	windowSize uint32,
	seqNum uint16,
) *packetBuilder {
	return &packetBuilder{
		packetType:   packetType,
		connID:       connID,
		tsMicros:     tsMicros,
		tsDiffMicros: 0,
		windowSize:   windowSize,
		seqNum:       seqNum,
		ackNum:       0,
	}
}

func (b *packetBuilder) WithTsDiffMicros(tsDiffMicros uint32) *packetBuilder {
	b.tsDiffMicros = tsDiffMicros
	return b
}

func (b *packetBuilder) WithAckNum(ackNum uint16) *packetBuilder {
	b.ackNum = ackNum
	return b
}

func (b *packetBuilder) WithSelectiveAck(selectiveAck *selectiveAck) *packetBuilder {
	b.selectiveAck = selectiveAck
	return b
}

func (b *packetBuilder) WithPayload(payload []byte) *packetBuilder {
	b.payload = payload
	return b
}

func (b *packetBuilder) Build() *packet {
	var headerExtension byte
	if b.selectiveAck != nil {
		headerExtension = 1
	}

	var payload []byte
	if b.payload != nil {
		payload = b.payload
	}

	ph := &packetWithHeader{
		h: packetHeaderV1{
			PacketType:    b.packetType,
			Version:       protocolVersionOne,
			Extension:     headerExtension,
			ConnectionId:  b.connID,
			Timestamp:     int64(b.tsMicros),
			TimestampDiff: b.tsDiffMicros,
			WndSize:       b.windowSize,
			SeqNum:        b.seqNum,
			AckNum:        b.ackNum,
		},
		p: packet{Eack: b.selectiveAck, Body: payload},
	}
	ph.p.Header = &ph.h
	return &ph.p
}
