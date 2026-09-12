# Vendored libutp

This directory contains BitTorrent Inc's libutp, unmodified, alongside a small
bridge that lets Go drive it.

It exists so the interoperability gate can run. Everything else in this
repository is conformance by citation -- code read against `utp_internal.cpp`
and matched by hand. That is worth a lot, but it is not evidence that a real
libutp peer will complete a transfer with us. These tests are.

## Provenance

| | |
| --- | --- |
| Upstream | https://github.com/bittorrent/libutp |
| Commit | `2b364cbb0650bdab64a5de2abb4518f9f228ec44` |
| Date | 2018-05-15 |
| Licence | MIT, in [`LICENSE.libutp`](LICENSE.libutp) |

This is the same commit [`REFERENCE.md`](../../REFERENCE.md) pins as the
normative text, and `scripts/check-libutp-reference.sh` checks it against the
copy vendored in `anacrolix/go-libutp`.

## What was vendored

The six translation units the upstream `Makefile` builds, plus the headers
they need:

```
utp_internal.cpp  utp_utils.cpp  utp_hash.cpp
utp_callbacks.cpp utp_api.cpp    utp_packedsockaddr.cpp

utp.h  utp_internal.h  utp_utils.h  utp_hash.h  utp_callbacks.h
utp_packedsockaddr.h  utp_templates.h  utp_types.h  libutp_inet_ntop.h
```

**Not modified.** The sources are byte-for-byte upstream. Compiler flags that
upstream sets in its `Makefile` (`-DPOSIX -fno-exceptions -fno-rtti
-Wno-sign-compare -fpermissive`) are passed through cgo instead.

**One omission:** `libutp_inet_ntop.cpp`, which supplies `inet_ntop` and
`inet_pton` for Windows versions before Vista. Upstream's `Makefile` does not
build it on POSIX either, and its header is still vendored because
`utp_packedsockaddr.cpp` includes it. Consequently this package is POSIX-only.

## Refreshing it

```sh
git clone https://github.com/bittorrent/libutp
cd libutp && git checkout <commit>
cp utp_internal.cpp utp_utils.cpp utp_hash.cpp utp_callbacks.cpp \
   utp_api.cpp utp_packedsockaddr.cpp \
   utp.h utp_internal.h utp_utils.h utp_hash.h utp_callbacks.h \
   utp_packedsockaddr.h utp_templates.h utp_types.h libutp_inet_ntop.h \
   LICENSE <this directory>/
mv <this directory>/LICENSE <this directory>/LICENSE.libutp
```

Then update the commit above, and `REFERENCE.md` if the normative pin moves
with it. libutp's last commit is from 2018, so this should not come up.

## The bridge

`bridge.h` / `bridge.cpp` give libutp a UDP socket and an event loop.

libutp does no I/O of its own -- it is an embedding library that calls back
out for everything, including the clock and the random number source. None of
those callbacks have defaults: an unset one returns zero, which would leave
every timestamp and connection id zero and nothing would work. The bridge
supplies all of them.

libutp is also not thread-safe, and that is broader than it looks. Every call
into it happens under the peer's own mutex, held by either the event loop or a
Go-facing entry point. One peer carries one connection, which is all an interop
test needs and keeps the lifetime rules -- particularly that a `utp_socket`
must not be touched after `UTP_STATE_DESTROYING` -- simple enough to get right.

A per-peer mutex is not enough. `utp_writev` keeps its working iovec in a
function-level `static` (`utp_internal.cpp:3156`), shared by every context in
the process, and `write_outgoing_packet` advances that array's pointers as it
copies payload out of it (`:1057-1066`). Two peers writing at once therefore
take each other's bytes. `global_lock.h` / `global_lock.cpp` add one recursive
process-wide lock around every entry into libutp, from peers and drivers
alike; `TestInteropConcurrentLibutpInitiators` is what found the need for it,
and removing the lock makes that test fail again immediately -- with libutp's
own `assert(needed == 0)` (`:1068`) rather than a wrong answer.

The lock lives outside the vendored sources: it is a property of how this
harness embeds libutp, not a patch to it.

## Build tags

The Go files require cgo. `nocgo.go` provides an empty package under
`!cgo`, so that `CGO_ENABLED=0 go build ./...` succeeds for the module --
building the library without cgo is the entire point of this fork, and a
package with no buildable Go files would break it.
