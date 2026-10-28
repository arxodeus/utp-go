package utp

import (
	"bytes"
	"testing"
)

const bufSize = 8192

func TestAvailable(t *testing.T) {
	buf := newSendBuffer(bufSize)
	if buf.Available() != bufSize {
		t.Errorf("Expected available size %d, got %d", bufSize, buf.Available())
	}

	const writeLen = 512
	const numWrites = 3
	const readLen = 64

	for i := 0; i < numWrites; i++ {
		data := make([]byte, writeLen)
		buf.Write(data)
	}
	expectedAvailable := bufSize - (writeLen * numWrites)
	if buf.Available() != expectedAvailable {
		t.Errorf("Expected available size %d, got %d", expectedAvailable, buf.Available())
	}

	data := make([]byte, readLen)
	buf.Read(data)
	expectedAvailable += readLen
	if buf.Available() != expectedAvailable {
		t.Errorf("Expected available size %d, got %d", expectedAvailable, buf.Available())
	}

	for i := 0; i < numWrites; i++ {
		data := make([]byte, writeLen)
		buf.Read(data)
	}
	if buf.Available() != bufSize {
		t.Errorf("Expected available size %d, got %d", bufSize, buf.Available())
	}
}

func TestRead(t *testing.T) {
	buf := newSendBuffer(bufSize)

	// Read of empty buffer returns zero.
	readBuf := make([]byte, bufSize)
	read := buf.Read(readBuf)
	if read != 0 {
		t.Errorf("Expected read size 0, got %d", read)
	}

	const writeLen = 1024
	const readLen = 784

	readBuf = make([]byte, readLen)

	writeOne := bytes.Repeat([]byte{0xef}, writeLen)
	writeTwo := bytes.Repeat([]byte{0xfe}, writeLen)
	buf.Write(writeOne)
	buf.Write(writeTwo)

	// Read first chunk of first write.
	read = buf.Read(readBuf)
	if read != readLen || !bytes.Equal(readBuf[:readLen], writeOne[:readLen]) {
		t.Errorf("First read failed, expected %d bytes, got %d", readLen, read)
	}

	// The rest of the first write, and the second continues it: a read
	// fills its buffer across writes (TestSendBufferReadSpansWrites).
	readBuf = make([]byte, readLen)
	read = buf.Read(readBuf)
	want := append(append([]byte(nil), writeOne[readLen:]...), writeTwo[:2*readLen-writeLen]...)
	if read != readLen || !bytes.Equal(readBuf[:read], want) {
		t.Errorf("Second read failed, expected %d bytes spanning both writes, got %d", readLen, read)
	}

	// What is left of the second write.
	read = buf.Read(readBuf)
	if rest := 2*writeLen - 2*readLen; read != rest || !bytes.Equal(readBuf[:read], writeTwo[2*readLen-writeLen:]) {
		t.Errorf("Third read failed, expected %d bytes, got %d", rest, read)
	}

	// Read with empty buffer returns zero.
	empty := make([]byte, 0)
	read = buf.Read(empty)
	if read != 0 {
		t.Errorf("Expected read size 0, got %d", read)
	}
}

func TestWrite(t *testing.T) {
	buf := newSendBuffer(bufSize)

	const writeLen = 1024

	data := bytes.Repeat([]byte{0xef}, writeLen)
	written := buf.Write(data)
	if written != writeLen {
		t.Errorf("Expected written size %d, got %d", writeLen, written)
	}

	if len(buf.pending) == 0 || !bytes.Equal(buf.pending[0], data) {
		t.Errorf("Data in buffer does not match written data")
	}
}

// The buffer owns the bytes it holds. Stream.Write returns as soon as
// the data is accepted here, and io.Writer implementations must not retain
// p, so callers reuse their buffers at once.
func TestSendBufferDoesNotRetainCallerSlice(t *testing.T) {
	sb := newSendBuffer(1024)

	caller := []byte("the original bytes")
	if n := sb.Write(caller); n != len(caller) {
		t.Fatalf("wrote %d of %d bytes", n, len(caller))
	}

	// The caller reuses its array, as io.Copy does between iterations.
	for i := range caller {
		caller[i] = 'X'
	}

	out := make([]byte, 64)
	n := sb.Read(out)
	if got := string(out[:n]); got != "the original bytes" {
		t.Errorf("read back %q, want %q: the buffer kept a reference to the caller's array",
			got, "the original bytes")
	}
}

// The same, for a write the buffer can only take part of: the accepted part
// must be copied too.
func TestSendBufferDoesNotRetainOnPartialWrite(t *testing.T) {
	sb := newSendBuffer(8)

	caller := []byte("0123456789abcdef")
	n := sb.Write(caller)
	if n != 8 {
		t.Fatalf("wrote %d bytes into an 8-byte buffer", n)
	}

	for i := range caller {
		caller[i] = 'X'
	}

	out := make([]byte, 64)
	got := sb.Read(out)
	if string(out[:got]) != "01234567" {
		t.Errorf("read back %q, want %q", out[:got], "01234567")
	}
}

// A read fills its buffer across writes, completing the last unsent packet
// before starting another.
func TestSendBufferReadSpansWrites(t *testing.T) {
	sb := newSendBuffer(1024)
	for _, w := range []string{"abc", "defg", "hi"} {
		sb.Write([]byte(w))
	}
	out := make([]byte, 6)
	if n := sb.Read(out); string(out[:n]) != "abcdef" {
		t.Errorf("first read %q, want %q", out[:n], "abcdef")
	}
	if n := sb.Read(out); string(out[:n]) != "ghi" {
		t.Errorf("second read %q, want %q", out[:n], "ghi")
	}
	if n := sb.Read(out); n != 0 || !sb.IsEmpty() || sb.Pending() != 0 {
		t.Errorf("third read %d bytes, empty %v, pending %d", n, sb.IsEmpty(), sb.Pending())
	}
}

// Take hands out a payload lying within one write as that write's own bytes,
// and copies only one that spans two. The shared slice is capped at its end,
// so appending to one payload cannot overwrite the next.
func TestSendBufferTakeSharesAWrite(t *testing.T) {
	sb := newSendBuffer(1 << 16)
	first := bytes.Repeat([]byte{1}, 1000)
	second := bytes.Repeat([]byte{2}, 1000)
	sb.Write(first)
	sb.Write(second)

	a := sb.Take(600)
	if &a[0] != &sb.pending[0][0] {
		t.Fatalf("a payload within one write was copied")
	}
	b := sb.Take(600) // 400 from the first write, 200 from the second
	c := sb.Take(800)
	if len(a) != 600 || len(b) != 600 || len(c) != 800 || sb.Pending() != 0 {
		t.Fatalf("took %d, %d, %d with %d left; expected 600, 600, 800 and none",
			len(a), len(b), len(c), sb.Pending())
	}
	if want := append(bytes.Repeat([]byte{1}, 400), bytes.Repeat([]byte{2}, 200)...); !bytes.Equal(b, want) {
		t.Fatalf("the payload across two writes is wrong")
	}
	if cap(a) != len(a) {
		t.Fatalf("a shared payload has capacity %d past its length %d", cap(a), len(a))
	}
	_ = append(a, 9)
	if b[0] != 1 {
		t.Fatalf("appending to one payload overwrote the next")
	}
	if !bytes.Equal(c, bytes.Repeat([]byte{2}, 800)) {
		t.Fatalf("the last payload is wrong")
	}
	if got := sb.Available(); got != 1<<16 {
		t.Fatalf("%d bytes available once everything was taken, expected %d", got, 1<<16)
	}
}
