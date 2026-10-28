package utp

import "testing"

// Readable lets a reader size its buffer to the data, and must distinguish
// an empty buffer from one holding only out-of-order bytes.
func TestReceiveBufferReadable(t *testing.T) {
	const initSeq = 100
	rb := newReceiveBuffer(64*1024, initSeq)

	if rb.Readable() != 0 {
		t.Errorf("a fresh buffer reports %d readable bytes, want 0", rb.Readable())
	}

	// A packet past the gap is held, not readable: the bytes before it have
	// not arrived.
	if err := rb.Write([]byte("second"), initSeq+2); err != nil {
		t.Fatalf("write out of order: %v", err)
	}
	if rb.IsEmpty() {
		t.Error("a buffer holding an out-of-order packet reports itself empty")
	}
	if got := rb.Readable(); got != 0 {
		t.Errorf("%d bytes reported readable while the gap before them is open, want 0", got)
	}

	// Fill the gap. Both packets become readable at once.
	if err := rb.Write([]byte("first!"), initSeq+1); err != nil {
		t.Fatalf("write in order: %v", err)
	}
	if got, want := rb.Readable(), len("first!")+len("second"); got != want {
		t.Errorf("%d bytes readable after the gap closed, want %d", got, want)
	}

	// It tracks what is left after a partial read.
	buf := make([]byte, 6)
	if n := rb.Read(buf); n != 6 || string(buf) != "first!" {
		t.Fatalf("read returned %d bytes %q", n, buf)
	}
	if got, want := rb.Readable(), len("second"); got != want {
		t.Errorf("%d bytes readable after reading six, want %d", got, want)
	}
}
