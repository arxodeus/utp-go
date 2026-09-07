package utp_go

import (
	"bytes"
	"testing"
)

const SIZE = 8192

func TestAvailable(t *testing.T) {
	buf := newSendBuffer(SIZE)
	if buf.Available() != SIZE {
		t.Errorf("Expected available size %d, got %d", SIZE, buf.Available())
	}

	const WRITE_LEN = 512
	const NUM_WRITES = 3
	const READ_LEN = 64

	for i := 0; i < NUM_WRITES; i++ {
		data := make([]byte, WRITE_LEN)
		buf.Write(data)
	}
	expectedAvailable := SIZE - (WRITE_LEN * NUM_WRITES)
	if buf.Available() != expectedAvailable {
		t.Errorf("Expected available size %d, got %d", expectedAvailable, buf.Available())
	}

	data := make([]byte, READ_LEN)
	buf.Read(data)
	expectedAvailable += READ_LEN
	if buf.Available() != expectedAvailable {
		t.Errorf("Expected available size %d, got %d", expectedAvailable, buf.Available())
	}

	for i := 0; i < NUM_WRITES; i++ {
		data := make([]byte, WRITE_LEN)
		buf.Read(data)
	}
	if buf.Available() != SIZE {
		t.Errorf("Expected available size %d, got %d", SIZE, buf.Available())
	}
}

func TestRead(t *testing.T) {
	buf := newSendBuffer(SIZE)

	// Read of empty buffer returns zero.
	readBuf := make([]byte, SIZE)
	read := buf.Read(readBuf)
	if read != 0 {
		t.Errorf("Expected read size 0, got %d", read)
	}

	const WRITE_LEN = 1024
	const READ_LEN = 784

	readBuf = make([]byte, READ_LEN)

	writeOne := bytes.Repeat([]byte{0xef}, WRITE_LEN)
	writeTwo := bytes.Repeat([]byte{0xfe}, WRITE_LEN)
	buf.Write(writeOne)
	buf.Write(writeTwo)

	// Read first chunk of first write.
	read = buf.Read(readBuf)
	if read != READ_LEN || !bytes.Equal(readBuf[:READ_LEN], writeOne[:READ_LEN]) {
		t.Errorf("First read failed, expected %d bytes, got %d", READ_LEN, read)
	}

	// Read remaining chunk of first write.
	readBuf = make([]byte, READ_LEN)
	read = buf.Read(readBuf)
	if read != WRITE_LEN-READ_LEN || !bytes.Equal(readBuf[:WRITE_LEN-READ_LEN], writeOne[READ_LEN:]) {
		t.Errorf("Second read failed, expected %d bytes, got %d", WRITE_LEN-READ_LEN, read)
	}

	// Read first chunk of second write.
	read = buf.Read(readBuf)
	if read != READ_LEN || !bytes.Equal(readBuf[:READ_LEN], writeTwo[:READ_LEN]) {
		t.Errorf("Third read failed, expected %d bytes, got %d", READ_LEN, read)
	}

	// Read with empty buffer returns zero.
	empty := make([]byte, 0)
	read = buf.Read(empty)
	if read != 0 {
		t.Errorf("Expected read size 0, got %d", read)
	}
}

func TestWrite(t *testing.T) {
	buf := newSendBuffer(SIZE)

	const WRITE_LEN = 1024

	data := bytes.Repeat([]byte{0xef}, WRITE_LEN)
	written := buf.Write(data)
	if written != WRITE_LEN {
		t.Errorf("Expected written size %d, got %d", WRITE_LEN, written)
	}

	if len(buf.pending) == 0 || !bytes.Equal(buf.pending[0], data) {
		t.Errorf("Data in buffer does not match written data")
	}
}

// The buffer must own the bytes it holds. UtpStream.Write returns as soon as
// the data is accepted here, so the caller is free to reuse its array the
// moment it returns -- and io.Writer's contract ("Implementations must not
// retain p") means every stdlib-shaped caller does exactly that.
//
// This buffer used to store the caller's slice by reference. Any full-duplex
// exchange -- read into a buffer, write it back, repeat -- had its queued
// bytes overwritten by the next read.
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
