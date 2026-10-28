//go:build linux

package utp

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// recvmmsgSlotCount is how many datagrams one recvmmsg can take.
const recvmmsgSlotCount = 16

// recvmmsgSlotSize holds any UDP datagram. The slots reserve 1 MiB of
// address space, but only the pages a datagram is written into are touched.
const recvmmsgSlotSize = 65536

// mmsghdr is Linux's struct mmsghdr: a msghdr and the length received.
type mmsghdr struct {
	hdr unix.Msghdr
	n   uint32
}

// drainState is what drain receives into.
type drainState struct {
	mmsg *recvmmsgSlots
}

// recvmmsgSlots are the buffers drain receives into, allocated once per
// udpConn. Only the read loop reads, so one set is enough.
type recvmmsgSlots struct {
	hdrs  [recvmmsgSlotCount]mmsghdr
	iovs  [recvmmsgSlotCount]unix.Iovec
	names [recvmmsgSlotCount]unix.RawSockaddrAny
	bufs  []byte
}

func newRecvmmsgSlots() *recvmmsgSlots {
	s := &recvmmsgSlots{bufs: make([]byte, recvmmsgSlotCount*recvmmsgSlotSize)}
	for i := range s.hdrs {
		s.iovs[i].Base = &s.bufs[i*recvmmsgSlotSize]
		s.iovs[i].SetLen(recvmmsgSlotSize)
		s.hdrs[i].hdr.Name = (*byte)(unsafe.Pointer(&s.names[i]))
		s.hdrs[i].hdr.Iov = &s.iovs[i]
		s.hdrs[i].hdr.SetIovlen(1)
	}
	return s
}

// drain appends the datagrams queued on the socket to out, up to sixteen per
// system call, waiting for the first one if none is queued. It reads until
// the socket would block rather than stopping at a short read, so datagrams
// arriving meanwhile join this batch.
func (c *udpConn) drain(rc syscall.RawConn, out *[]datagram) error {
	if c.mmsg == nil {
		c.mmsg = newRecvmmsgSlots()
	}
	s := c.mmsg
	var readErr error
	err := rc.Read(func(fd uintptr) bool {
		for len(*out) < maxReadBatch {
			vlen := min(recvmmsgSlotCount, maxReadBatch-len(*out))
			for i := 0; i < vlen; i++ {
				s.hdrs[i].hdr.Namelen = uint32(unsafe.Sizeof(s.names[i]))
				s.hdrs[i].hdr.Flags = 0
				s.hdrs[i].n = 0
			}
			r, _, errno := unix.Syscall6(unix.SYS_RECVMMSG, fd,
				uintptr(unsafe.Pointer(&s.hdrs[0])), uintptr(vlen), 0, 0, 0)
			if errno != 0 {
				switch errno {
				case unix.EINTR:
					continue
				case unix.EAGAIN:
					// Nothing queued: wait, unless this call has already read something.
					return len(*out) > 0
				}
				readErr = errno
				return true
			}
			got := int(r)
			for i := 0; i < got; i++ {
				h := &s.hdrs[i]
				if h.hdr.Flags&unix.MSG_TRUNC != 0 {
					// Larger than any UDP datagram; cannot happen.
					continue
				}
				k, ok := rawPeerKey(&s.names[i])
				if !ok {
					continue
				}
				peer := c.peerForKey(k)
				n := int(h.n)
				payload := c.lend(s.bufs[i*recvmmsgSlotSize : i*recvmmsgSlotSize+n])
				*out = append(*out, datagram{payload: payload, peer: peer, borrowed: true})
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	return readErr
}

// rawPeerKey returns the peer cache key for a raw source address.
func rawPeerKey(sa *unix.RawSockaddrAny) (peerKey, bool) {
	var k peerKey
	switch sa.Addr.Family {
	case unix.AF_INET:
		a := (*unix.RawSockaddrInet4)(unsafe.Pointer(sa))
		p := (*[2]byte)(unsafe.Pointer(&a.Port))
		copy(k.ip[:], a.Addr[:])
		k.port = int(p[0])<<8 | int(p[1])
	case unix.AF_INET6:
		a := (*unix.RawSockaddrInet6)(unsafe.Pointer(sa))
		p := (*[2]byte)(unsafe.Pointer(&a.Port))
		k.ip, k.zone, k.v6 = a.Addr, a.Scope_id, true
		k.port = int(p[0])<<8 | int(p[1])
	default:
		return peerKey{}, false
	}
	return k, true
}
