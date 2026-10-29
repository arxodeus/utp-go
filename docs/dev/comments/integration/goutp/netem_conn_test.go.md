# integration/goutp/netem_conn_test.go

Every comment in `integration/goutp/netem_conn_test.go` at e56c9aa, verbatim.

## Line 11 — type netemAddr

```
// netemAddr is a netem peer as a net.Addr.
```

## Line 17 — type netemPacketConn

```
// netemPacketConn runs purego over a netem endpoint: purego takes a
// net.PacketConn and uses ReadFrom, WriteTo, LocalAddr and Close of it.
```

## Line 21 — type netemPacketConn

```
// by name, for resolving dial addresses
```

## Line 54 — func (*netemPacketConn).resolve

```
// resolve is purego's address resolver for this network: a dial address is
// an endpoint's name.
```
