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
	// maxKnownExtension is the highest known extension type: 0 none,
	// 1 selective ack, 2 extension bits.
	maxKnownExtension = 2
	zeroMoment        = time.Duration(0)
	acksArrayLength   = byte(4)
	packetHeaderLen   = 20
	selectiveAckBits  = 32
	extensionTypeLen  = 1
	extensionLenLen   = 1
)

const (
	stData packetType = iota
	stFin
	stState
	stReset
	stSyn
)

var (
	errInvalidHeaderSize           = errors.New("utp: invalid header size")
	errUnsupportedVersion          = errors.New("utp: unsupported protocol version")
	errUnknownExtension            = errors.New("utp: unknown extension type")
	errInvalidPacketType           = errors.New("utp: invalid packet type")
	errInsufficientLen             = errors.New("utp: insufficient length for extension")
	errInsufficientSelectiveAckLen = errors.New("utp: insufficient length for selective ACK")
	errInvalidSelectiveAckLen      = errors.New("utp: invalid length for selective ACK")
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
	case stData:
		return "stData"
	case stFin:
		return "stFin"
	case stState:
		return "stState"
	case stReset:
		return "stReset"
	case stSyn:
		return "stSyn"
	default:
		return "UNKNOWN"
	}
}

type packetHeaderV1 struct {
	PacketType    packetType
	Version       byte
	Extension     byte
	ConnectionID  uint16
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
	b = binary.BigEndian.AppendUint16(b, h.ConnectionID)      // 2
	b = binary.BigEndian.AppendUint32(b, uint32(h.Timestamp)) // 4
	b = binary.BigEndian.AppendUint32(b, h.TimestampDiff)     // 4
	b = binary.BigEndian.AppendUint32(b, h.WndSize)           // 4
	b = binary.BigEndian.AppendUint16(b, h.SeqNum)            // 2
	b = binary.BigEndian.AppendUint16(b, h.AckNum)            // 2
	return b
}

// IsPacket reports whether b looks like a uTP packet, as far as its header
// can tell: its length, packet type, version and first extension type. A
// datagram it accepts is one the socket can parse, and one it rejects is one
// the socket would drop. utpnet uses it to tell uTP from other protocols
// sharing the port.
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

// decodePacketHeaderInto decodes a packet header into hdr.
func decodePacketHeaderInto(h *packetHeaderV1, value []byte) error {
	if len(value) < minimalHeaderSize {
		return errInvalidHeaderSize
	}

	packetTypeVal := packetType(value[0] >> 4)
	if err := packetTypeVal.Check(); err != nil {
		return err
	}

	// Version 1 is the only version. Anything else is dropped.
	version := value[0] & 0x0F
	if version != protocolVersionOne {
		return errUnsupportedVersion
	}
	versionVal := version

	// The first extension must be a known type: 0 (none), 1 (selective
	// ack) or 2 (extension bits). A packet whose first extension is unknown
	// is dropped. An unknown extension later in the chain is skipped by its
	// length, as BEP 29 describes.
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
		ConnectionID:  connID,
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

// selectiveAck is a selective acknowledgment bitmask.
type selectiveAck struct {
	acked [][selectiveAckBits]bool
}

// newSelectiveAck returns a selective ack with a bit set for each true
// element of acked.
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

// Encode returns the selective-ack bitmask. Bits are least significant
// first within each byte: the lowest bit of the first byte represents
// ack_nr+2, the next ack_nr+3, and so on, as BEP 29 specifies.
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

// decodeSelectiveAck decodes a selective-ack bitmask.
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
	// chain is the extension chain of a decoded packet, in order, and nil for
	// one built locally. See wireExtensions.
	chain []extensionData
	// borrowed reports that Body aliases the socket reader's buffer, which
	// the next read reuses. Anything that keeps the packet beyond the call
	// that received it must call own first.
	borrowed bool
}

// own gives the packet its own copy of a borrowed body.
//
// Most packets are processed before the reader reads again, so in-order data
// is copied once, from the read buffer into the receive buffer. Only a
// packet kept for later, such as one queued for a connection's loop or a SYN
// waiting for an Accept, copies its body here.
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

// malformedExtensionBits reports an extension-bits extension whose length is
// not 8. Such a packet is rejected.
func (p *packet) malformedExtensionBits() bool {
	for _, e := range p.chain {
		if e.extension == extensionBits && len(e.payload) != 8 {
			return true
		}
	}
	return false
}

// wireExtensions returns the extension chain Encode writes, in order.
//
// A packet built locally carries at most a selective ack. A decoded packet
// carries every extension it arrived with, in order, so that decoding and
// re-encoding yields the same bytes, including unknown extensions and
// extension 2, whose contents are not interpreted.
//
// The selective ack keeps its place in the chain and its bytes come from
// Eack, so changing Eack changes what is written. Its place is the last
// type-1 extension, the one the decoder reads. If Eack is nil that entry is
// dropped, unless it was empty on arrival and so never became an Eack.
//
// An unknown type is never written first, since a packet whose first
// extension is unknown is rejected. That can only arise if a caller cleared
// a leading selective ack; unknown extensions before the first known one are
// then dropped.
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

// Encode returns the packet's wire form. The header's extension byte is
// derived from the extensions actually written, so the output always decodes
// again.
func (p *packet) Encode() []byte {
	return p.appendEncoded(make([]byte, 0, p.EncodedLen()))
}

// appendEncoded appends the packet's wire form to bytes.
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

	// Append the payload.
	bytes = append(bytes, p.Body...)

	return bytes
}

// decodePacket decodes one datagram. If borrowed is set, b is the reader's
// buffer, reused after dispatch: the packet's body still refers to it and is
// copied by own when the packet is kept. Nothing else in the packet refers to
// b.
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
	// A zero-length ST_DATA is valid and is acknowledged like any other.
	// Every extension is kept in order (see wireExtensions), so the
	// header's extension byte stays as it arrived and agrees with what
	// Encode writes.
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
		// Need two bytes for the extension header.
		if len(data[index:]) < 2 {
			return nil, 0, errInsufficientLen
		}

		// Type of the next extension.
		nextExt := data[index]

		// Length of this extension.
		extLen := int(data[index+1])

		// Calculate start of extension data
		extStart := index + 2

		// Need the whole extension.
		if len(data[extStart:]) < extLen {
			return nil, 0, errInsufficientLen
		}

		// Copy the extension data.
		extData := make([]byte, extLen)
		copy(extData, data[extStart:extStart+extLen])

		// Add it to the list.
		extensions = append(extensions, extensionData{
			extension: ext,
			payload:   extData,
		})

		// Move to the next extension.
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

func (b *packetBuilder) WithTimestampDiffMicros(tsDiffMicros uint32) *packetBuilder {
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
			ConnectionID:  b.connID,
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
