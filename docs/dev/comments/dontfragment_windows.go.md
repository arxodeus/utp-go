# dontfragment_windows.go

Every comment in `dontfragment_windows.go` at e56c9aa, verbatim.

## Line 8 — const windowsIPDontFragment, windowsIPv6DontFrag

```
// Windows' don't-fragment options, which the standard library's syscall
// package does not define. Both are from ws2ipdef.h: IP_DONTFRAGMENT at the
// IPPROTO_IP level and IPV6_DONTFRAG at IPPROTO_IPV6. They share the value 14
// at their respective levels, which looks like a typo and is not.
```

## Line 17 — func setDontFragment

```
// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket. See the Linux file for why this is a socket
// option toggled around a single send, and why succeeding if either address
// family works is the right test.
```
