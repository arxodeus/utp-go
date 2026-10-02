package utp_go

import (
	"encoding/binary"
	"errors"
	"time"
)

const (
	MINIMAL_HEADER_SIZE                    = 20
	MINIMAL_HEADER_SIZE_WITH_SELECTIVE_ACK = 26
	PROTOCOL_VERSION_ONE                   = 1
	// MAX_KNOWN_EXTENSION is the highest extension type either implementation
	// recognises: 0 none, 1 selective ack, 2 extension bits.
	MAX_KNOWN_EXTENSION = 2
	ZERO_MOMENT         = time.Duration(0)
	ACKS_ARRAY_LENGTH   = byte(4)
	PACKET_HEADER_LEN   = 20
	SELECTIVE_ACK_BITS  = 32
	EXTENSION_TYPE_LEN  = 1
	EXTENSION_LEN_LEN   = 1
)

const (
	st_data PacketType = iota
	st_fin
	st_state
	st_reset
	st_syn
)

var (
	ErrInvalidHeaderSize           = errors.New("invalid header size")
	ErrUnsupportedVersion          = errors.New("unsupported protocol version")
	ErrUnknownExtension            = errors.New("unknown extension type")
	ErrInvalidPacketVersion        = errors.New("invalid packet version")
	ErrInvalidPacketType           = errors.New("invalid packet type")
	ErrInvalidExtensionType        = errors.New("invalid extension type")
	ErrInsufficientLen             = errors.New("insufficient length for extension")
	ErrPacketTooShort              = errors.New("packet too short for selective ack extension")
	ErrInsufficientSelectiveAckLen = errors.New("insufficient length for selective ACK")
	ErrInvalidSelectiveAckLen      = errors.New("invalid length for selective ACK")
)

type PacketType byte

func (p PacketType) Check() error {
	if p > 4 {
		return ErrInvalidPacketType
	}
	return nil
}

func (p *PacketType) String() string {
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

type PacketHeaderV1 struct {
	PacketType    PacketType
	Version       byte
	Extension     byte
	ConnectionId  uint16
	Timestamp     int64
	TimestampDiff uint32
	WndSize       uint32
	SeqNum        uint16
	AckNum        uint16
}

func (h *PacketHeaderV1) encodeTypeVer() byte {
	typeVer := byte(h.PacketType)<<4 | h.Version
	return typeVer
}

func (h *PacketHeaderV1) EncodeToBytes() []byte {
	return h.appendTo(make([]byte, 0, MINIMAL_HEADER_SIZE))
}

// appendTo appends the header's 20 bytes to b.
func (h *PacketHeaderV1) appendTo(b []byte) []byte {
	b = append(b, h.encodeTypeVer(), h.Extension)             // 2
	b = binary.BigEndian.AppendUint16(b, h.ConnectionId)      // 2
	b = binary.BigEndian.AppendUint32(b, uint32(h.Timestamp)) // 4
	b = binary.BigEndian.AppendUint32(b, h.TimestampDiff)     // 4
	b = binary.BigEndian.AppendUint32(b, h.WndSize)           // 4
	b = binary.BigEndian.AppendUint16(b, h.SeqNum)            // 2
	b = binary.BigEndian.AppendUint16(b, h.AckNum)            // 2
	return b
}

func DecodePacketHeader(value []byte) (*PacketHeaderV1, error) {
	h := new(PacketHeaderV1)
	if err := decodePacketHeaderInto(h, value); err != nil {
		return nil, err
	}
	return h, nil
}

// decodePacketHeaderInto is DecodePacketHeader into a header the caller
// provides.
func decodePacketHeaderInto(h *PacketHeaderV1, value []byte) error {
	if len(value) < MINIMAL_HEADER_SIZE {
		return ErrInvalidHeaderSize
	}

	packetType := value[0] >> 4
	packetTypeVal := PacketType(packetType)
	if err := packetTypeVal.Check(); err != nil {
		return err
	}

	// Version 1 is the only version either this implementation or libutp
	// speaks. libutp drops anything else outright (utp_internal.cpp:2834),
	// and accepting what the reference rejects is both an incompatibility and
	// an attack surface: it lets a peer reach the rest of this decoder with a
	// packet no real implementation would have sent.
	version := value[0] & 0x0F
	if version != PROTOCOL_VERSION_ONE {
		return ErrUnsupportedVersion
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
	if extension > MAX_KNOWN_EXTENSION {
		return ErrUnknownExtension
	}

	connID := binary.BigEndian.Uint16(value[2:4])
	tsMicros := binary.BigEndian.Uint32(value[4:8])
	tsDiffMicros := binary.BigEndian.Uint32(value[8:12])
	windowSize := binary.BigEndian.Uint32(value[12:16])
	seqNum := binary.BigEndian.Uint16(value[16:18])
	ackNum := binary.BigEndian.Uint16(value[18:20])

	*h = PacketHeaderV1{
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
	h PacketHeaderV1
}

// SelectiveAck represents a selective acknowledgment
type SelectiveAck struct {
	acked [][SELECTIVE_ACK_BITS]bool
}

// NewSelectiveAck creates a new SelectiveAck from a slice of booleans
func NewSelectiveAck(acked []bool) *SelectiveAck {
	chunks := len(acked) / SELECTIVE_ACK_BITS
	remainder := len(acked) % SELECTIVE_ACK_BITS

	ack := &SelectiveAck{acked: make([][SELECTIVE_ACK_BITS]bool, chunks)}
	for i := 0; i < chunks; i++ {
		var fragment [32]bool
		copy(fragment[:], acked[i*SELECTIVE_ACK_BITS:(i+1)*SELECTIVE_ACK_BITS])
		ack.acked[i] = fragment
	}

	if remainder > 0 {
		var fragment [32]bool
		copy(fragment[:], acked[chunks*SELECTIVE_ACK_BITS:])
		ack.acked = append(ack.acked, fragment)
	}

	return ack
}

func (s *SelectiveAck) EncodedLen() int {
	return (len(s.acked) * SELECTIVE_ACK_BITS) / 8
}

func (s *SelectiveAck) Acked() []bool {
	result := make([]bool, 0, len(s.acked)*SELECTIVE_ACK_BITS)
	for _, boolArray := range s.acked {
		result = append(result, boolArray[:]...)
	}
	return result
}

// Encode encodes the SelectiveAck into a byte slice
// Encode renders the selective-ack bitmask.
//
// Bit order is least-significant-first within each byte: the LSB of the first
// byte represents ack_nr+2, the next bit ack_nr+3, and so on. That is what
// BEP 29 specifies and what libutp emits -- it builds the mask with
// `m |= 1 << i` for the i'th packet past ack_nr+2 and writes the low byte
// first (utp_internal.cpp:806-818).
//
// This previously set `1 << (7-j)`, reversing the bits within every byte. The
// decoder below reversed them the same way, so this implementation agreed
// with itself and Go-to-Go transfers were unaffected -- but every selective
// ack it sent was misread by any real peer, and every one it received was
// misread in turn. Only a comparison against libutp surfaces a bug that is
// self-consistent.
func (s *SelectiveAck) Encode() []byte {
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

// DecodeSelectiveAck decodes a byte slice into a SelectiveAck
func DecodeSelectiveAck(data []byte) (*SelectiveAck, error) {
	if len(data) < 4 {
		return nil, ErrInsufficientSelectiveAckLen
	}
	if len(data)%4 != 0 {
		return nil, ErrInvalidSelectiveAckLen
	}

	acked := make([][SELECTIVE_ACK_BITS]bool, len(data)/4)
	for i := 0; i < len(data); i += 4 {
		var tmp [SELECTIVE_ACK_BITS]bool
		for j := 0; j < 4; j++ {
			byteVal := data[i+j]
			for k := 0; k < 8; k++ {
				// Least-significant bit first, matching Encode and BEP 29.
				tmp[j*8+k] = (byteVal & (1 << k)) != 0
			}
		}
		acked[i/4] = tmp
	}

	return &SelectiveAck{acked: acked}, nil
}

type SelectiveAckExtension [4]byte

type packet struct {
	Header *PacketHeaderV1
	Eack   *SelectiveAck
	Body   []byte
	// chain is the extension chain as it arrived, in order, for a decoded
	// packet, and nil for one built here. See wireExtensions.
	chain []ExtensionData
}

func (p *packet) EncodedLen() int {
	length := MINIMAL_HEADER_SIZE
	for _, e := range p.wireExtensions() {
		length += EXTENSION_TYPE_LEN + EXTENSION_LEN_LEN + len(e.payload)
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
// reads. These used to be dropped. Nothing on the live path re-encodes a
// received packet, and libutp never does, but DecodePacket is exported, and
// anything that forwarded packets through it would have stripped what it did
// not understand.
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
// used to come first, and then the unknown extensions ahead of the first
// known one are dropped.
func (p *packet) wireExtensions() []ExtensionData {
	if len(p.chain) == 0 {
		if p.Eack != nil {
			return []ExtensionData{{extension: 1, payload: p.Eack.Encode()}}
		}
		return nil
	}
	slot := -1
	for i, e := range p.chain {
		if e.extension == 1 {
			slot = i
		}
	}
	out := make([]ExtensionData, 0, len(p.chain)+1)
	if slot < 0 && p.Eack != nil {
		out = append(out, ExtensionData{extension: 1, payload: p.Eack.Encode()})
	}
	for i, e := range p.chain {
		if i == slot {
			switch {
			case p.Eack != nil:
				e = ExtensionData{extension: 1, payload: p.Eack.Encode()}
			case len(e.payload) != 0:
				continue
			}
		}
		out = append(out, e)
	}
	for len(out) > 0 && out[0].extension > MAX_KNOWN_EXTENSION {
		out = out[1:]
	}
	return out
}

// Encode writes the packet to the wire.
//
// The header's extension byte is derived from what is actually being written
// rather than taken from p.Header, so that everything this produces can be
// decoded again.
//
// It used to be taken from p.Header, which is only correct for packets built
// by PacketBuilder. A *decoded* packet keeps whatever extension byte the peer
// sent, while Eack is nil unless a selective ack was retained -- so a packet
// that arrived carrying a zero-length selective ack, or the extension-bits
// extension, re-encoded to a header claiming an extension with no extension
// bytes after it. This implementation's own decoder rejects that, with
// "insufficient length for extension". Found by FuzzDecodePacket.
//
// Nothing on the live path re-encodes a decoded packet today, so this was
// latent -- but DecodePacket is exported and its result has exported methods,
// so an external caller could reach it, and a proxy or relay built on this
// library would have emitted unparseable packets.
func (p *packet) Encode() []byte {
	bytes := make([]byte, 0, p.EncodedLen())

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

// extensionByte is the extension this packet actually carries first, as
// opposed to the one its header claims.
func (p *packet) extensionByte() byte {
	if exts := p.wireExtensions(); len(exts) > 0 {
		return exts[0].extension
	}
	return 0
}

func DecodePacket(b []byte) (*packet, error) {
	receivedBytesLength := len(b)
	if receivedBytesLength < MINIMAL_HEADER_SIZE {
		return nil, ErrInvalidHeaderSize
	}

	ph := &packetWithHeader{}
	p := &ph.p
	header := &ph.h
	if err := decodePacketHeaderInto(header, b[:MINIMAL_HEADER_SIZE]); err != nil {
		return nil, err
	}

	extensions, extensionsLen, err := DecodeRawExtensions(header.Extension, b[MINIMAL_HEADER_SIZE:])
	if err != nil {
		return nil, err
	}

	var extension ExtensionData
	for _, extensionData := range extensions {
		if extensionData.extension == 1 {
			extension = extensionData
		}
	}
	var ack *SelectiveAck
	if len(extension.payload) != 0 {
		if ack, err = DecodeSelectiveAck(extension.payload); err != nil {
			return nil, err
		}
	}

	payloadStartIndex := MINIMAL_HEADER_SIZE + extensionsLen
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
	return p, nil
	//var body []byte
	//if header.Extension == 0 {
	//	if receivedBytesLength == MINIMAL_HEADER_SIZE {
	//		body = []byte{}
	//	} else {
	//		body = b[MINIMAL_HEADER_SIZE:]
	//	}
	//	p.Header = header
	//	p.Eack = nil
	//	p.Body = body
	//	return &p, nil
	//}
	//if receivedBytesLength < MINIMAL_HEADER_SIZE_WITH_SELECTIVE_ACK {
	//	return nil, ErrPacketTooShort
	//}
	//nextExtension := b[20]
	//extLength := b[21]
	//
	//if nextExtension != 0 || extLength != 4 {
	//	return nil, fmt.Errorf("bad format of selective ack extension: extension=%d, len=%d", nextExtension, extLength)
	//}
	//
	//extension, err := DecodeSelectiveAck(b[22:26])
	//if err != nil {
	//	return nil, ErrInvalidSelectiveAckLen
	//}

	//if receivedBytesLength == MINIMAL_HEADER_SIZE_WITH_SELECTIVE_ACK {
	//	body = []byte{}
	//} else {
	//	body = b[MINIMAL_HEADER_SIZE_WITH_SELECTIVE_ACK:]
	//}
	//p.Header = header
	//p.Eack = extension
	//p.Body = body
	//return &p, nil
}

type ExtensionData struct {
	extension byte
	payload   []byte
}

func DecodeRawExtensions(firstExt byte, data []byte) ([]ExtensionData, int, error) {
	ext := firstExt
	index := 0
	extensions := make([]ExtensionData, 0)

	for ext != 0 {
		// Check if enough data for header (2 bytes)
		if len(data[index:]) < 2 {
			return nil, 0, ErrInsufficientLen
		}

		// Read next extension type
		nextExt := data[index]

		// Read extension length
		extLen := int(data[index+1])

		// Calculate start of extension data
		extStart := index + 2

		// Check if enough data for extension content
		if len(data[extStart:]) < extLen {
			return nil, 0, ErrInsufficientLen
		}

		// Copy extension data
		extData := make([]byte, extLen)
		copy(extData, data[extStart:extStart+extLen])

		// Add to extensions list
		extensions = append(extensions, ExtensionData{
			extension: ext,
			payload:   extData,
		})

		// Move to next extension
		ext = nextExt
		index = extStart + extLen
	}

	return extensions, index, nil
}

type PacketBuilder struct {
	packetType   PacketType
	connID       uint16
	tsMicros     uint32
	tsDiffMicros uint32
	windowSize   uint32
	seqNum       uint16
	ackNum       uint16
	selectiveAck *SelectiveAck
	payload      []byte
}

func NewPacketBuilder(
	packetType PacketType,
	connID uint16,
	tsMicros uint32,
	windowSize uint32,
	seqNum uint16,
) *PacketBuilder {
	return &PacketBuilder{
		packetType:   packetType,
		connID:       connID,
		tsMicros:     tsMicros,
		tsDiffMicros: 0,
		windowSize:   windowSize,
		seqNum:       seqNum,
		ackNum:       0,
	}
}

func (b *PacketBuilder) WithTsMicros(tsMicros uint32) *PacketBuilder {
	b.tsMicros = tsMicros
	return b
}

func (b *PacketBuilder) WithTsDiffMicros(tsDiffMicros uint32) *PacketBuilder {
	b.tsDiffMicros = tsDiffMicros
	return b
}

func (b *PacketBuilder) WithWindowSize(windowSize uint32) *PacketBuilder {
	b.windowSize = windowSize
	return b
}

func (b *PacketBuilder) WithAckNum(ackNum uint16) *PacketBuilder {
	b.ackNum = ackNum
	return b
}

func (b *PacketBuilder) WithSelectiveAck(selectiveAck *SelectiveAck) *PacketBuilder {
	b.selectiveAck = selectiveAck
	return b
}

func (b *PacketBuilder) WithPayload(payload []byte) *PacketBuilder {
	b.payload = payload
	return b
}

func (b *PacketBuilder) Build() *packet {
	var headerExtension byte
	if b.selectiveAck != nil {
		headerExtension = 1
	}

	var payload []byte
	if b.payload != nil {
		payload = b.payload
	}

	ph := &packetWithHeader{
		h: PacketHeaderV1{
			PacketType:    b.packetType,
			Version:       PROTOCOL_VERSION_ONE,
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
