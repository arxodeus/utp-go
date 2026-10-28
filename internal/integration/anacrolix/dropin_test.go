package anacrolix

import (
	"net"
	"reflect"
	"testing"

	"github.com/anacrolix/dht/v2"
	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
)

// The drop-in replacement for go-libutp, through torrent's own socket layer.
//
// This module's go.mod replaces github.com/anacrolix/go-libutp with
// compat/golibutp, which is all an application does to use it. torrent's
// default build (cgo on, no tags) then makes its uTP sockets with that
// package's NewSocket, in its own listenUtp, on the same port as its TCP
// listener would be, installs its firewall with SetSyncFirewallCallback, and
// runs its DHT on the same socket. Nothing here calls this library directly:
// the test is that torrent, configured as an application would configure it,
// transfers a torrent and answers DHT queries over it.
func TestTorrentOverTheGoLibutpDropIn(t *testing.T) {
	var clients []*torrent.Client
	transferTorrent(t, func(t *testing.T, dir string, seed bool) *torrent.Client {
		cl := newDefaultUtpClient(t, dir, seed)
		clients = append(clients, cl)
		return cl
	})

	// The DHT shares the uTP socket's port: one client's DHT server pings
	// the other's, and the reply comes back through the socket that is also
	// carrying uTP.
	from, to := dhtServer(t, clients[0]), dhtServer(t, clients[1])
	res := from.Ping(to.Addr().(*net.UDPAddr))
	if res.Err != nil {
		t.Fatalf("DHT ping from %v to %v over the shared socket: %v", from.Addr(), to.Addr(), res.Err)
	}
	if !sameAddr(clients[1].ListenAddrs(), to.Addr()) {
		t.Fatalf("the DHT is on %v, not on the uTP socket's port %v", to.Addr(), clients[1].ListenAddrs())
	}
}

// newDefaultUtpClient is a torrent.Client with torrent's own uTP and DHT on,
// TCP off so uTP is the only transport between peers, and nothing that
// reaches beyond this host.
func newDefaultUtpClient(t *testing.T, dataDir string, seed bool) *torrent.Client {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = seed
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.DisableTCP = true
	cfg.DisableIPv6 = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.NoDefaultPortForwarding = true
	cfg.DhtStartingNodes = func(string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) { return nil, nil }
	}
	cfg.PeriodicallyAnnounceTorrentsToDht = false
	cfg.Logger = alog.Default.FilterLevel(alog.Critical)

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("creating the torrent client: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	// torrent's listener is its utpSocketSocket around the uTP socket it
	// made; that socket must be the drop-in's, with torrent's firewall
	// installed. The drop-in's Socket holds a *utpnet.Socket where
	// go-libutp's holds a libutp context.
	listeners := cl.Listeners()
	if len(listeners) != 1 {
		t.Fatalf("want torrent's one uTP listener, have %d", len(listeners))
	}
	inner := reflect.ValueOf(listeners[0]).Field(0).Elem()
	sock := inner.Elem()
	if f := sock.FieldByName("us"); !f.IsValid() || f.Type().String() != "*utpnet.Socket" {
		t.Fatalf("torrent's uTP socket is a %v, not the drop-in's", inner.Type())
	}
	if f := sock.FieldByName("syncFirewallCallback"); f.IsNil() {
		t.Fatalf("torrent did not install its firewall callback")
	}
	return cl
}

func dhtServer(t *testing.T, cl *torrent.Client) *dht.Server {
	t.Helper()
	servers := cl.DhtServers()
	if len(servers) != 1 {
		t.Fatalf("want one DHT server, have %d", len(servers))
	}
	return servers[0].(torrent.AnacrolixDhtServerWrapper).Server
}

func sameAddr(addrs []net.Addr, a net.Addr) bool {
	for _, b := range addrs {
		if b.String() == a.String() {
			return true
		}
	}
	return false
}
