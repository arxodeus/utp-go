# Reference implementation: what is authoritative, and proof the copies agree

uTP has no adequate written specification. BEP 29 is a summary, not a
specification, and it is silent on most of what actually determines whether
two peers interoperate: retransmission schedules, the give-up rules, what to
do with a malformed header, which packet is sent when. The real specification
is **libutp's behaviour**, because that is what the peers you meet are
running.

This document pins which copy of libutp is normative, which copy we can link
from Go for differential testing, and proves the two are protocol-identical.

## The two copies

| Role | Repository | Version | Commit |
| --- | --- | --- | --- |
| **Normative text** | [`bittorrent/libutp`](https://github.com/bittorrent/libutp) | default branch | `2b364cbb0650bdab64a5de2abb4518f9f228ec44` (2018-05-15) |
| **Linkable copy** | [`anacrolix/go-libutp`](https://github.com/anacrolix/go-libutp) | `v1.3.1` | `8e6d012495c632e5e80571ef3eeb5b5846ce6cd8` (2023-07-19) |

`bittorrent/libutp` is BitTorrent Inc's canonical implementation, the ancestor
of what µTorrent and every mainstream client run. It is effectively frozen —
its last commit is 2018-05-15 — so the target does not move.

`anacrolix/go-libutp` vendors a copy of that C source. Because it is a cgo
package, a Go test binary can link it and compare our implementation against
the real C code directly. That is what makes "match libutp" checkable rather
than aspirational — but only if the vendored copy really is the canonical one.

## Why this has to be checked, not assumed

If `go-libutp` is bumped, or the vendored C ever drifts, we would be
conforming to a fork while believing we match the canonical source. The
failure mode is silent: the differential tests would still pass, against the
wrong reference.

## The comparison

Every C source file, diffed between the two copies. `substantive` ignores
whitespace-only and blank-line-only changes (`diff -w -B`).

| File | Differing lines | Substantive |
| --- | --- | --- |
| `utp_internal.cpp` | 18 | 8 (4 changed lines) |
| `utp_internal.h` | 2 | 2 (1 changed line) |
| `utp.h` | 4 | 4 (4 added lines) |
| `utp_api.cpp` | 0 | 0 |
| `utp_utils.cpp` | 0 | 0 |
| `utp_utils.h` | 0 | 0 |
| `utp_hash.cpp` | 0 | 0 |
| `utp_hash.h` | 0 | 0 |
| `utp_packedsockaddr.cpp` | 0 | 0 |
| `utp_packedsockaddr.h` | 0 | 0 |
| `utp_types.h` | 0 | 0 |
| `utp_callbacks.cpp` | 0 | 0 |
| `utp_callbacks.h` | 0 | 0 |
| `utp_templates.h` | 0 | 0 |

The complete set of differences is recorded verbatim in
[`scripts/libutp-expected.diff`](scripts/libutp-expected.diff). There are
three, and none of them touches the wire:

1. **`utp_internal.cpp` — `send_to_addr` gained a `utp_socket*` parameter**
   so the `sendto` callback learns which socket sent the packet. This changes
   the embedding API. It does not change a single byte on the wire.
2. **`utp_internal.h` — `memset((void*)this, 0, sizeof(*this))`.** A cast
   added to silence a compiler warning.
3. **`utp.h` — a Windows `<stdint.h>` include.** Portability.

The remaining raw-line differences are trailing whitespace removals.

### Protocol constants

Every protocol constant is identical in both copies:

| Constant | Value |
| --- | --- |
| `CCONTROL_TARGET` | `100 * 1000` µs |
| `MAX_CWND_INCREASE_BYTES_PER_RTT` | `3000` |
| `CUR_DELAY_SIZE` | `3` |
| `DELAY_BASE_HISTORY` | `13` |
| `MAX_WINDOW_DECAY` | `100` ms |
| `REORDER_BUFFER_MAX_SIZE` | `1024` |
| `MIN_WINDOW_SIZE` | `10` |
| `DUPLICATE_ACKS_BEFORE_RESEND` | `3` |
| `ACK_NR_ALLOWED_WINDOW` | `DUPLICATE_ACKS_BEFORE_RESEND` |
| `KEEPALIVE_INTERVAL` | `29000` ms |
| `PACKET_SIZE` | `1435` |

## Reproducing this

```
scripts/check-libutp-reference.sh [workdir]
```

The script fetches both pinned commits, diffs every file, compares the result
byte-for-byte against `scripts/libutp-expected.diff`, and compares every
protocol constant. It exits non-zero if the two copies have diverged in any
way beyond the three recorded differences — so a future dependency bump that
introduces a protocol-affecting change fails loudly instead of silently
redirecting what we conform to.

It needs network access to fetch the two repositories; pass a workdir to reuse
existing clones.

Last run: **PASS** — exactly the recorded API/portability differences, all
protocol constants equal.

## Licence

libutp is MIT licensed. Anything derived from it here honours that.
