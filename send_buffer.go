package utp_go

type sendBuffer struct {
	pending [][]byte
	offset  int
	size    int
	// used is the length of everything in pending, the consumed front
	// included. Available and Pending are asked on every packet composed, and
	// summed the chunks each time: quadratic in a run of small writes.
	used int
}

func newSendBuffer(size int) *sendBuffer {
	return &sendBuffer{
		pending: make([][]byte, 0),
		offset:  0,
		size:    size,
	}
}

func (sb *sendBuffer) Available() int {
	return sb.size + sb.offset - sb.used
}

// Pending reports how many bytes of application data are buffered but not
// yet handed to the connection for transmission.
func (sb *sendBuffer) Pending() int {
	return sb.used - sb.offset
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
	sb.used += n
	return n
}

// Read fills buf from the front of the buffer, across as many writes as it
// takes, and reports how much it copied.
//
// It used to stop at the end of the write it started in, so a packet never
// held bytes from two writes. libutp fills the last unsent packet before it
// starts another (write_outgoing_packet, utp_internal.cpp:1013-1023): a run of
// small writes queued together becomes full packets, and a bulk writer whose
// writes are not a multiple of the packet size does not leave a short packet
// at the end of every one.
func (sb *sendBuffer) Read(buf []byte) int {
	n := 0
	for n < len(buf) && len(sb.pending) > 0 {
		data := sb.pending[0]
		k := copy(buf[n:], data[sb.offset:])
		n += k
		if sb.offset+k == len(data) {
			sb.dropFront()
		} else {
			sb.offset += k
		}
	}
	return n
}

// Take removes the next n bytes, or as many as there are, and returns them as
// a packet's payload.
//
// A payload that lies within one write is that write's own bytes, not a copy
// of them: the write was already copied in, by Write, and nothing changes it
// after. libutp copies the application's bytes once, into the packet
// (write_outgoing_packet, utp_internal.cpp:1061); copying again here made
// it twice, and one allocation for every packet sent. The slice is capped, so
// appending to it cannot reach the bytes after it.
//
// The write stays in memory until the last packet taken from it is
// acknowledged, rather than shrinking packet by packet. That holds at most a
// write's worth more than the window, and a write is at most the buffer.
func (sb *sendBuffer) Take(n int) []byte {
	if len(sb.pending) == 0 || n <= 0 {
		return nil
	}
	front := sb.pending[0]
	if rest := len(front) - sb.offset; rest >= n {
		out := front[sb.offset : sb.offset+n : sb.offset+n]
		if rest == n {
			sb.dropFront()
		} else {
			sb.offset += n
		}
		return out
	}
	out := make([]byte, min(n, sb.Pending()))
	return out[:sb.Read(out)]
}

func (sb *sendBuffer) dropFront() {
	sb.used -= len(sb.pending[0])
	sb.pending[0] = nil
	sb.pending = sb.pending[1:]
	sb.offset = 0
}
