# dontfragment_freebsd.go

Every comment in `dontfragment_freebsd.go` at e56c9aa, verbatim.

## Line 8 — func setDontFragment

```
// setDontFragment turns the don't-fragment bit on or off for the next
// datagram sent on this socket. FreeBSD defines both options in the standard
// library's syscall package, so unlike Darwin nothing has to be spelled out
// here. See the Linux file for why this is a socket option toggled around a
// single send, and why succeeding if either address family works is the right
// test.
```
