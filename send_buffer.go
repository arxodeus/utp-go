package utp_go

type sendBuffer struct {
	pending [][]byte
	offset  int
	size    int
}

func newSendBuffer(size int) *sendBuffer {
	return &sendBuffer{
		pending: make([][]byte, 0),
		offset:  0,
		size:    size,
	}
}

func (sb *sendBuffer) Available() int {
	used := 0
	for _, data := range sb.pending {
		used += len(data)
	}
	return sb.size + sb.offset - used
}

// Pending reports how many bytes of application data are buffered but not
// yet handed to the connection for transmission.
func (sb *sendBuffer) Pending() int {
	used := 0
	for _, data := range sb.pending {
		used += len(data)
	}
	return used - sb.offset
}

func (sb *sendBuffer) IsEmpty() bool {
	return len(sb.pending) == 0
}

// Write copies data into the buffer and reports how much it took.
//
// The copy is not an optimisation to remove. This buffer used to retain the
// caller's slice, and UtpStream.Write returns as soon as the bytes are
// accepted here -- before they are transmitted. So the caller got control back
// while the connection still held a reference to their array, and anything
// that reuses its buffer between writes had its queued bytes overwritten with
// whatever it read next.
//
// That is not an exotic pattern: io.Writer's contract says "Implementations
// must not retain p", and io.Copy, bufio.Writer and every echo loop rely on
// it. It showed up as corrupted payloads in any full-duplex exchange -- read
// into a buffer, write it back, repeat -- while every unidirectional test in
// this repository passed, because they each write one buffer once and never
// touch it again.
func (sb *sendBuffer) Write(data []byte) int {
	available := sb.Available()
	n := len(data)
	if n > available {
		n = available
	}
	if n <= 0 {
		return 0
	}
	owned := make([]byte, n)
	copy(owned, data[:n])
	sb.pending = append(sb.pending, owned)
	return n
}

func (sb *sendBuffer) Read(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}

	if len(sb.pending) == 0 {
		return 0
	}

	data := sb.pending[0]
	n := minInt(len(data)-sb.offset, len(buf))
	copy(buf, data[sb.offset:sb.offset+n])

	if sb.offset+n == len(data) {
		sb.offset = 0
		sb.pending = sb.pending[1:]
	} else {
		sb.offset += n
	}

	return n
}
