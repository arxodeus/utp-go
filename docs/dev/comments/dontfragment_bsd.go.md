# dontfragment_bsd.go

Every comment in `dontfragment_bsd.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build netbsd || openbsd
```

## Line 10 — func setDontFragment

```
// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket.
//
// NetBSD and OpenBSD are IPv6-only here. Both define IPV6_DONTFRAG, and
// neither defines an IPv4 IP_DONTFRAG: on those systems the bit is not
// settable per socket for IPv4 at all. Guessing a number for it would be
// worse than not offering it -- socket option numbers are not shared across
// these kernels, as Darwin's 0x1c against FreeBSD's 0x43 shows, and a wrong
// guess would quietly set some unrelated option.
//
// So an IPv4 socket here returns ErrDontFragmentUnsupported and the probe
// goes out fragmentable, exactly as it did before any of this existed. An
// IPv6 socket gets the real thing, which is the case that needs it least,
// since IPv6 routers do not fragment in the first place.
```
