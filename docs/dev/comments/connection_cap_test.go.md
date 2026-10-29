# connection_cap_test.go

Every comment in `connection_cap_test.go` at e56c9aa, verbatim.

## Line 9

```
// The incoming-connection cap: libutp refuses a SYN when its context already
// holds more than 3000 sockets (utp_internal.cpp:2967-2974). See
// WithMaxConnections.
```

## Line 13 — func capTestSocket

```
// capTestSocket is a socket on a scripted transport with the given options,
// and a function that sends it a SYN for a new connection id.
```

## Line 28 — func settled

```
// settled waits until the socket has dealt with every SYN sent so far, which
// is when each has either been parked or refused.
```

## Line 37 — func TestConnectionCapRefusesPastTheCap

```
// Past the cap a SYN is refused, silently, and nothing is created for it. The
// comparison is libutp's: refused while the socket holds more than the cap,
// so the cap itself admits one more.
```

## Line 69 — func TestConnectionCapIgnoresARetransmittedSyn

```
// A SYN for a connection already parked is the peer retransmitting it, not a
// new connection, and is not refused however full the socket is.
```

## Line 90 — func TestConnectionCapFreesAsConnectionsGo

```
// Capacity comes back when a connection goes, because the count is of what
// the socket holds now.
```

## Line 118 — func TestConnectionCapCountsDialledConnections

```
// Connections this socket dialled count, as every socket in a libutp context
// does.
```

## Line 140 — func TestConnectionCapCanBeRemoved

```
// A negative cap is no cap.
```

## Line 152 — func TestConnectionCapDefaultsToLibutps

```
// Without the option, and with it set to zero, the cap is libutp's.
```
