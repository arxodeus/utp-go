# Vendored libutp

BitTorrent's libutp, unmodified, with a small bridge that lets Go tests drive
it as a peer over real UDP sockets. It is used only by tests and is not
linked into the library.

## Provenance

| | |
| --- | --- |
| Upstream | https://github.com/bittorrent/libutp |
| Commit | `2b364cbb0650bdab64a5de2abb4518f9f228ec44` |
| Date | 2018-05-15 |
| Licence | MIT, in [`LICENSE.libutp`](LICENSE.libutp) |

`check-reference.sh` pins the same commit and checks it
against the copy vendored in `anacrolix/go-libutp`.

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

Then update the commit above and the pin in
`check-reference.sh`.

## The bridge

`bridge.h` and `bridge.cpp` give libutp a UDP socket, an event loop, and the
callbacks it needs, including the clock and random source; libutp has no
defaults for them. Each peer carries one connection, under the peer's own
mutex.

libutp is not thread-safe across contexts either: `utp_writev` keeps its
iovec in a function-level `static`. `global_lock.h` and `global_lock.cpp`
therefore take one process-wide recursive lock around every call into
libutp. The lock is part of the harness, not a patch to libutp.

## Build tags

The Go files require cgo. `nocgo.go` provides an empty package under
`!cgo`, so that `CGO_ENABLED=0 go build ./...` succeeds for the module.
