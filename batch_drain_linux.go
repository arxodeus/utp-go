//go:build linux

package utp_go

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// recvmmsgSlotCount is how many datagrams one recvmmsg can take.
const recvmmsgSlotCount = 16

// recvmmsgSlotSize holds any UDP datagram whole: the largest IPv4 payload is
// 65,507 bytes and the largest IPv6 one without a jumbogram 65,527, and
// MaxPacketSize may be set that high on loopback, whose path MTU is 65,488.
// The slots are 1 MiB of address space, but the kernel writes only what
// arrives and fresh pages are not zeroed, so a full-size packet uses its
// slot's first page and nothing more is ever touched.
const recvmmsgSlotSize = 65536

// mmsghdr is Linux's struct mmsghdr: a msghdr and the length received. Go
// pads it to the word size as C does (TestMmsghdrLayout).
type mmsghdr struct {
	hdr unix.Msghdr
	n   uint32
}

// recvmmsgSlots is what drain receives into, made once per UdpConn. Only the
// socket's read loop reads, so one set is enough.
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

// drain appends what the socket holds to out, waiting for the first datagram
// if there is none: libutp's embedder reads until the socket would block, and
// this does the same, up to sixteen datagrams a system call.
//
// Until it would block, not until a read comes back short. A short read has
// emptied the queue, and stopping there saves the last call, the one that
// only reports there is nothing left -- 0.3-0.8 us of a datagram's way to its
// acknowledgement, too little for TestAckTurnaround to see. But datagrams
// arriving meanwhile then start the next batch instead of joining this one,
// so under load batches were smaller than libutp's (TestManyConcurrentTransfers:
// 18.7-24.5 datagrams against 22.9-29.6), and every batch ends with an
// acknowledgement for each connection it reached.
func (c *UdpConn) drain(rc syscall.RawConn, out *[]datagram) error {
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
					// Nothing yet: wait, unless something has come.
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

// rawPeerKey is the peer cache's key for a source address as the kernel
// wrote it, without making a syscall.Sockaddr of it first.
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
