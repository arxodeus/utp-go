#!/usr/bin/env bash
#
# M0 gate: prove the canonical libutp and the copy vendored in
# anacrolix/go-libutp are protocol-identical.
#
# We differential-test against go-libutp's vendored C, but the normative text
# is bittorrent/libutp. If a dependency bump ever introduces a
# protocol-affecting difference between the two, we would be conforming to a
# fork while believing we match the canonical source. This script fails when
# that happens.
#
# Usage: scripts/check-libutp-reference.sh [workdir]
# Exit status 0 = the two copies are protocol-identical.

set -euo pipefail

# --- pinned references (see REFERENCE.md) ------------------------------------
LIBUTP_URL="https://github.com/bittorrent/libutp.git"
LIBUTP_COMMIT="2b364cbb0650bdab64a5de2abb4518f9f228ec44"   # 2018-05-15

GOLIBUTP_URL="https://github.com/anacrolix/go-libutp.git"
GOLIBUTP_TAG="v1.3.1"
GOLIBUTP_COMMIT="8e6d012495c632e5e80571ef3eeb5b5846ce6cd8"  # 2023-07-19

# Every C source file that carries protocol behaviour.
FILES=(
  utp_internal.cpp utp_internal.h utp.h utp_api.cpp
  utp_utils.cpp utp_utils.h utp_hash.cpp utp_hash.h
  utp_packedsockaddr.cpp utp_packedsockaddr.h utp_types.h
  utp_callbacks.cpp utp_callbacks.h utp_templates.h
)

# Protocol constants that must be identical in both copies.
CONSTANTS=(
  CCONTROL_TARGET
  MAX_CWND_INCREASE_BYTES_PER_RTT
  CUR_DELAY_SIZE
  DELAY_BASE_HISTORY
  MAX_WINDOW_DECAY
  REORDER_BUFFER_MAX_SIZE
  MIN_WINDOW_SIZE
  DUPLICATE_ACKS_BEFORE_RESEND
  ACK_NR_ALLOWED_WINDOW
  KEEPALIVE_INTERVAL
  PACKET_SIZE
)

# The complete set of accepted differences is recorded in
# scripts/libutp-expected.diff. It is compared byte for byte, so any new
# difference -- however small -- fails this gate rather than slipping under a
# threshold. The recorded differences are:
#
#   utp_internal.cpp  send_to_addr gained a `utp_socket*` parameter so the
#                     sendto callback learns which socket sent the packet.
#                     An embedding-API change; not a byte on the wire.
#   utp_internal.h    memset((void*)this, ...) -- a compiler-warning cast.
#   utp.h             a Windows <stdint.h> include.
#
EXPECTED_DIFF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/libutp-expected.diff"

WORK="${1:-$(mktemp -d)}"
mkdir -p "$WORK"

fetch() { # url commit dest
  local url="$1" commit="$2" dest="$3"
  if [ ! -d "$dest/.git" ]; then
    echo "  fetching $url"
    git init -q "$dest"
    git -C "$dest" remote add origin "$url"
  fi
  if ! git -C "$dest" cat-file -e "$commit^{commit}" 2>/dev/null; then
    git -C "$dest" fetch -q --depth 1 origin "$commit"
  fi
  git -C "$dest" checkout -q "$commit"
}

echo "== fetching pinned references into $WORK"
fetch "$LIBUTP_URL"   "$LIBUTP_COMMIT"   "$WORK/libutp"
fetch "$GOLIBUTP_URL" "$GOLIBUTP_COMMIT" "$WORK/go-libutp"

# Confirm the go-libutp commit really is the tag we claim.
if ! git -C "$WORK/go-libutp" describe --tags --exact-match "$GOLIBUTP_COMMIT" 2>/dev/null | grep -qx "$GOLIBUTP_TAG"; then
  echo "  note: could not confirm $GOLIBUTP_COMMIT is tag $GOLIBUTP_TAG (shallow clone)"
fi

status=0

echo
echo "== per-file differences"
actual="$WORK/actual.diff"
: > "$actual"
for f in "${FILES[@]}"; do
  a="$WORK/libutp/$f"
  b="$WORK/go-libutp/$f"
  if [ ! -f "$a" ] || [ ! -f "$b" ]; then
    printf '  %-26s MISSING (libutp:%s go-libutp:%s)\n' "$f" \
      "$([ -f "$a" ] && echo yes || echo no)" "$([ -f "$b" ] && echo yes || echo no)"
    status=1
    continue
  fi
  raw=$(diff "$a" "$b" | grep -cE '^[<>]' || true)
  # -w -B ignores whitespace-only and blank-line-only changes.
  d=$(diff -w -B "$a" "$b" || true)
  sub=$(printf '%s' "$d" | grep -cE '^[<>]' || true)
  if [ -n "$d" ]; then
    printf -- '--- %s\n%s\n' "$f" "$d" >> "$actual"
  fi
  printf '  %-26s %3s differing lines (%s substantive)\n' "$f" "$raw" "$sub"
done

echo
echo "== substantive differences vs. the recorded set"
if [ ! -f "$EXPECTED_DIFF" ]; then
  echo "  FAIL: missing $EXPECTED_DIFF"
  status=1
elif diff -q "$EXPECTED_DIFF" "$actual" >/dev/null 2>&1; then
  echo "  OK: exactly the recorded API/portability differences, nothing more."
else
  echo "  FAIL: the copies have diverged from the recorded set."
  echo "  Review before trusting any differential test against go-libutp:"
  diff "$EXPECTED_DIFF" "$actual" | sed 's|^|    |'
  status=1
fi

echo
echo "== protocol constants"
for c in "${CONSTANTS[@]}"; do
  va=$(grep -rhoE "^#define[[:space:]]+$c[[:space:]]+[^/]*" "$WORK/libutp"/*.cpp "$WORK/libutp"/*.h 2>/dev/null | head -1 | sed 's/[[:space:]]*$//' || true)
  vb=$(grep -rhoE "^#define[[:space:]]+$c[[:space:]]+[^/]*" "$WORK/go-libutp"/*.cpp "$WORK/go-libutp"/*.h 2>/dev/null | head -1 | sed 's/[[:space:]]*$//' || true)
  if [ -z "$va" ] && [ -z "$vb" ]; then
    printf '  %-34s not found in either copy (skipped)\n' "$c"
    continue
  fi
  if [ "$va" == "$vb" ]; then
    printf '  %-34s OK  %s\n' "$c" "${va#\#define }"
  else
    printf '  %-34s MISMATCH\n    libutp:    %s\n    go-libutp: %s\n' "$c" "$va" "$vb"
    status=1
  fi
done

echo
if [ "$status" -eq 0 ]; then
  echo "PASS: the two copies are protocol-identical."
else
  echo "FAIL: protocol-affecting difference detected. See REFERENCE.md."
fi
exit "$status"
