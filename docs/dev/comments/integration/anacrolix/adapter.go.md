# integration/anacrolix/adapter.go

Every comment in `integration/anacrolix/adapter.go` at e56c9aa, verbatim.

## Line 11 — type Socket

```
// Socket is the interface github.com/anacrolix/torrent requires of a uTP
// implementation. It is copied from that package's unexported `utpSocket`
// (utp.go), which cannot be referred to from outside it.
//
// Copying an interface is normally a way to be wrong quietly: the copy drifts
// and the check keeps passing. That is why NewUtpSocket below is written to
// be assignable to the real thing -- see the compile-time assertion in
// adapter_check.go, which uses torrent.NewUtpSocket's own signature and so
// fails if the real interface changes shape.
```

## Line 27 — func NewUtpSocket

```
// NewUtpSocket has the signature torrent.NewUtpSocket has, so it can replace
// it. The firewall callback is accepted and ignored; see the note in
// adapter_check.go.
```

## Line 31 — func NewUtpSocket

```
// The firewall callback is honoured rather than accepted and dropped.
	//
	// It used to be ignored, which is the worst of the three options: a caller
	// that passed a blocklist got no blocking and no indication that it was
	// not happening. torrent uses this to refuse connections it has decided
	// against before any state exists for them, which is exactly what
	// libutp's UTP_ON_FIREWALL is for.
```

## Line 50 — type FirewallCallback

```
// FirewallCallback mirrors torrent's unexported firewallCallback: it is asked
// whether a connection from an address should be refused before any state is
// created for it.
```

## Line 55 — func udpNetwork

```
// udpNetwork maps torrent's network names onto the UDP ones.
```
