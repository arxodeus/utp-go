//go:build linux

package utp_go

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mmsghdr is laid out as the kernel's struct mmsghdr: a msghdr, the length
// received, and padding to the word size. Written for any word size: a
// 32-bit build has no padding at all.
func TestMmsghdrLayout(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	want := (unsafe.Sizeof(unix.Msghdr{}) + 4 + word - 1) / word * word
	if got := unsafe.Sizeof(mmsghdr{}); got != want {
		t.Fatalf("mmsghdr is %d bytes, the kernel's %d", got, want)
	}
	if off := unsafe.Offsetof(mmsghdr{}.n); off != unsafe.Sizeof(unix.Msghdr{}) {
		t.Fatalf("msg_len at offset %d, want %d", off, unsafe.Sizeof(unix.Msghdr{}))
	}
}

// rawPeerKey reads an address as the kernel writes it -- the port in network
// byte order -- into the key peerFor makes from the same address.
func TestRawPeerKeyMatchesPeerFor(t *testing.T) {
	var any4 unix.RawSockaddrAny
	a4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(&any4))
	a4.Family = unix.AF_INET
	*(*[2]byte)(unsafe.Pointer(&a4.Port)) = [2]byte{0x1f, 0x90} // 8080
	a4.Addr = [4]byte{192, 0, 2, 7}

	var any6 unix.RawSockaddrAny
	a6 := (*unix.RawSockaddrInet6)(unsafe.Pointer(&any6))
	a6.Family = unix.AF_INET6
	*(*[2]byte)(unsafe.Pointer(&a6.Port)) = [2]byte{0xc3, 0x50} // 50000
	a6.Addr = [16]byte{0xfe, 0x80, 15: 1}
	a6.Scope_id = 3

	for _, tc := range []struct {
		raw  *unix.RawSockaddrAny
		want peerKey
	}{
		{&any4, peerKey{ip: [16]byte{192, 0, 2, 7}, port: 8080}},
		{&any6, peerKey{ip: [16]byte{0xfe, 0x80, 15: 1}, port: 50000, zone: 3, v6: true}},
	} {
		got, ok := rawPeerKey(tc.raw)
		if !ok || got != tc.want {
			t.Errorf("rawPeerKey: %+v (%v), want %+v", got, ok, tc.want)
		}
	}
	if _, ok := rawPeerKey(&unix.RawSockaddrAny{}); ok {
		t.Error("an address of no known family made a key")
	}
}
