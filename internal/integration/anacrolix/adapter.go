package anacrolix

import (
	"context"
	"net"

	alog "github.com/anacrolix/log"
	"github.com/arxodeus/utp/utpnet"
)

// Socket is the interface github.com/anacrolix/torrent requires of a uTP
// implementation. It is copied from that package's unexported `utpSocket`
// (utp.go), which cannot be referred to from outside it.
//
// TestSatisfiesTorrentsUTPSocketInterface checks utpnet.Socket against the
// real interface, found by reflection, so a drift in this copy is caught.
type Socket interface {
	net.PacketConn
	Accept() (net.Conn, error)
	Addr() net.Addr
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewSocket has the signature of torrent.NewUtpSocket, so it can replace
// it.
func NewSocket(network, addr string, fw FirewallCallback, logger alog.Logger) (Socket, error) {
	// The firewall callback is honored: torrent uses it to refuse
	// connections before any state exists for them.
	opts := &utpnet.Options{}
	if fw != nil {
		opts.Firewall = fw
	}
	sock, err := utpnet.Listen(context.Background(), udpNetwork(network), addr, opts)
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
