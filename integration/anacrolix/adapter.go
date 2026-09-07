package anacrolix

import (
	"context"
	"net"

	alog "github.com/anacrolix/log"
	"github.com/zen-eth/utp-go/utpnet"
)

// UtpSocket is the interface github.com/anacrolix/torrent requires of a uTP
// implementation. It is copied from that package's unexported `utpSocket`
// (utp.go), which cannot be referred to from outside it.
//
// Copying an interface is normally a way to be wrong quietly: the copy drifts
// and the check keeps passing. That is why NewUtpSocket below is written to
// be assignable to the real thing -- see the compile-time assertion in
// adapter_check.go, which uses torrent.NewUtpSocket's own signature and so
// fails if the real interface changes shape.
type UtpSocket interface {
	net.PacketConn
	Accept() (net.Conn, error)
	Addr() net.Addr
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewUtpSocket has the signature torrent.NewUtpSocket has, so it can replace
// it. The firewall callback is accepted and ignored; see the note in
// adapter_check.go.
func NewUtpSocket(network, addr string, _ FirewallCallback, logger alog.Logger) (UtpSocket, error) {
	sock, err := utpnet.Listen(context.Background(), udpNetwork(network), addr, &utpnet.Options{})
	if err != nil {
		return nil, err
	}
	_ = logger
	return sock, nil
}

// FirewallCallback mirrors torrent's unexported firewallCallback: it is asked
// whether a connection from an address should be refused before any state is
// created for it.
type FirewallCallback func(net.Addr) bool

// udpNetwork maps torrent's network names onto the UDP ones.
func udpNetwork(network string) string {
	switch network {
	case "utp", "udp", "":
		return "udp"
	case "utp4", "udp4":
		return "udp4"
	case "utp6", "udp6":
		return "udp6"
	default:
		return "udp"
	}
}
