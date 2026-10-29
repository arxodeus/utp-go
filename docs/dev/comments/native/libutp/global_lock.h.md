# native/libutp/global_lock.h

Every comment in `native/libutp/global_lock.h` at e56c9aa, verbatim.

## Line 1

```
// global_lock.h -- one lock for all of libutp in this process.
//
// libutp is documented as not thread-safe, and the usual reading of that is
// per-context: give each connection its own utp_context and its own thread and
// nothing is shared. That reading is wrong. utp_writev holds its working
// iovec in a function-level static:
//
//	ssize_t utp_writev(utp_socket *conn, struct utp_iovec *iovec_input, size_t num_iovecs)
//	{
//	    static utp_iovec iovec[UTP_IOV_MAX];
//	                                        (utp_internal.cpp:3154-3156)
//
// which every context in the process shares. write_outgoing_packet then walks
// that array and advances its pointers as it copies payload out of it
// (:1057-1066), so two threads writing at once take each other's bytes: the
// visible result is one connection's payload appearing inside another's
// stream, and, when the interleaving is unlucky, libutp's own
// `assert(needed == 0)` at :1068 firing because the iovec it was walking was
// reset under it.
//
// utp_utils.cpp has process-wide statics of its own -- the clock offset and
// its lazily initialised state at :86, :131, :159 and :199 -- so utp_writev is
// the one that corrupts data, not the only one that is shared.
//
// The consequence for the harness: every entry into libutp, from any peer or
// driver, is serialised on this lock. It is recursive because libutp calls
// back into the embedder from inside its own calls (UTP_STATE_WRITABLE during
// utp_process_udp, for instance) and the bridge answers some of those
// callbacks by calling libutp again.
//
// This costs the harness parallelism it never needed. It is worth stating
// plainly for anyone embedding libutp: separate contexts on separate threads
// are not enough.
```

## Line 48

```
// libutp_guard locks for the duration of a scope.
```

## Line 57

```
// LIBUTP_GLOBAL_LOCK_H
```
