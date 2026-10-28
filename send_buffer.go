package utp

type sendBuffer struct {
	pending [][]byte
	offset  int
	size    int
	// used is the length of everything in pending, including the consumed
	// front, so Available and Pending need not sum the chunks.
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

// Write copies data into the buffer and reports how much it took. The copy
// is required: UtpStream.Write returns once the bytes are accepted, before
// they are sent, and io.Writer implementations must not retain p.
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

// Adopt takes data into the buffer without copying it and reports how much
// it took. The buffer keeps data, so nothing else may modify it. The write
// path uses it for bytes it has already copied from the caller.
func (sb *sendBuffer) Adopt(data []byte) int {
	n := min(len(data), sb.Available())
	if n <= 0 {
		return 0
	}
	sb.pending = append(sb.pending, data[:n:n])
	sb.used += n
	return n
}

// Read fills buf from the front of the buffer, across as many writes as it
// takes, and reports how much it copied. Filling packets across writes turns
// a run of small writes into full packets.
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
// a packet's payload. A payload within one write aliases that write's bytes
// rather than copying them; the returned slice is capped, so appending to it
// cannot overwrite what follows.
//
// A write stays in memory until the last packet taken from it is
// acknowledged, which holds at most one write's worth beyond the window.
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
