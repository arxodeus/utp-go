package anacrolix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/zen-eth/utp-go/utpnet"
)

// A real torrent, transferred over this library and hash-verified by the
// client that received it.
//
// Everything else in this module is a check on shape: that *utpnet.Socket
// satisfies the interface torrent requires, and that it survives the traffic
// pattern a torrent client produces. Neither runs the protocol torrent
// actually speaks. This does: two torrent.Clients, a generated torrent, and
// BitTorrent's own piece hashes deciding whether what arrived is what was
// sent.
//
// The README said this was not covered because wiring it up needed a fork of
// torrent or a `replace`. That was true of torrent's built-in socket layer,
// which picks its uTP implementation at build time. It is not true of
// Client.AddListener and Client.AddDialer, which are exported and take
// exactly what utpnet.Socket already provides. Every built-in network is
// disabled here, so the only way a byte can move between these two clients is
// through this library.
func TestTorrentTransferOverUtp(t *testing.T) {
	const (
		fileSize    = 4 << 20 // 4 MiB, 128 pieces
		pieceLength = 32 << 10
	)

	seedDir := t.TempDir()
	leechDir := t.TempDir()
	name := "payload.bin"

	// Deterministic contents: a failure is reproducible, and the file is not
	// compressible enough for any layer to accidentally shorten it.
	payload := make([]byte, fileSize)
	rng := rand.New(rand.NewSource(20260912))
	if _, err := rng.Read(payload); err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(seedDir, name)
	if err := os.WriteFile(seedPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(payload)

	info := metainfo.Info{PieceLength: pieceLength}
	if err := info.BuildFromFilePath(seedPath); err != nil {
		t.Fatalf("building torrent info: %v", err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("encoding torrent info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	t.Logf("torrent: %d bytes in %d pieces of %d", info.TotalLength(), info.NumPieces(), info.PieceLength)

	seeder, seederSock := newUtpOnlyClient(t, seedDir, true)
	leecher, _ := newUtpOnlyClient(t, leechDir, false)

	st, err := seeder.AddTorrent(&mi)
	if err != nil {
		t.Fatalf("adding the torrent to the seeder: %v", err)
	}
	<-st.GotInfo()
	// The seeder has the data already; make it say so before anyone asks.
	st.VerifyData()

	lt, err := leecher.AddTorrent(&mi)
	if err != nil {
		t.Fatalf("adding the torrent to the leecher: %v", err)
	}
	<-lt.GotInfo()

	if n := lt.AddClientPeer(seeder); n == 0 {
		t.Fatalf("the leecher was given no peers; the seeder is listening on %v", seederSock.Addr())
	}

	start := time.Now()
	lt.DownloadAll()

	// Completion is two conditions, not one. `BytesCompleted == Length` alone
	// is true for a moment right after the torrent is added, before the
	// leecher has worked out that it has none of the pieces -- three runs in
	// five ended there, "complete" in 240ms with no file on disk. Requiring
	// that the whole payload also arrived as peer data is what makes this a
	// measurement of a transfer rather than of a race.
	deadline := time.After(120 * time.Second)
	for {
		stats := lt.Stats()
		read := stats.BytesReadData.Int64()
		if lt.BytesCompleted() == lt.Length() && read >= lt.Length() {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("the transfer stalled: %d of %d bytes complete, %d bytes read from peers "+
				"after %v, %d active peer connection(s)",
				lt.BytesCompleted(), lt.Length(), read,
				time.Since(start).Round(time.Second), len(lt.PeerConns()))
		case <-time.After(20 * time.Millisecond):
		}
	}
	elapsed := time.Since(start)
	finalStats := lt.Stats()
	readOverUtp := finalStats.BytesReadData.Int64()

	// Read it back through the client rather than off the disk.
	//
	// The default storage writes to "<name>.part" and renames only once the
	// whole torrent is complete, which happens after the last piece is marked
	// complete rather than with it -- so reading the final path here found no
	// file in four runs out of five, on a transfer that had in fact finished.
	// The reader is what a consumer of the library uses, and it returns what
	// the client will serve: pieces that passed the metainfo's own hashes.
	reader := lt.NewReader()
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading the completed torrent back: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("the leecher has %d bytes, the seeder had %d", len(got), len(payload))
	}
	if sum := sha256.Sum256(got); sum != want {
		t.Fatalf("the transferred data does not match: got %s, want %s",
			hex.EncodeToString(sum[:]), hex.EncodeToString(want[:]))
	}

	// And the same bytes on disk, once the storage has renamed its part file.
	// Bounded, and not the assertion this test turns on: what matters is that
	// the transfer completed and verified, not how quickly anacrolix renames.
	finalPath := filepath.Join(leechDir, name)
	for waited := time.Duration(0); waited < 30*time.Second; waited += 50 * time.Millisecond {
		if _, err := os.Stat(finalPath); err == nil {
			onDisk, err := os.ReadFile(finalPath)
			if err != nil {
				t.Fatalf("reading %s: %v", finalPath, err)
			}
			if sum := sha256.Sum256(onDisk); sum != want {
				t.Fatalf("the file on disk does not match what was transferred: %s",
					hex.EncodeToString(sum[:]))
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Logf("%d bytes transferred over uTP and hash-verified in %v (%.1f Mbps); %d bytes of "+
		"that arrived as peer data over this library",
		len(got), elapsed.Round(time.Millisecond),
		float64(len(got))*8/elapsed.Seconds()/1e6, readOverUtp)
}

// newUtpOnlyClient builds a torrent.Client that can reach the network only
// through this library.
//
// Every built-in listener is disabled -- TCP, torrent's own uTP, the DHT --
// so torrent creates no sockets of its own and `listenAll` is asked for no
// networks. The socket added afterwards is a *utpnet.Socket, used both as the
// listener torrent accepts on and, wrapped in a NetworkDialer, as the dialer
// it connects out with. If this library did not work, there would be no
// transport at all rather than a quiet fallback to TCP.
func newUtpOnlyClient(t *testing.T, dataDir string, seed bool) (*torrent.Client, *utpnet.Socket) {
	t.Helper()

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = seed
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.NoDefaultPortForwarding = true
	cfg.Logger = alog.Default.FilterLevel(alog.Critical)

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("creating the torrent client: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	sock, err := utpnet.Listen(context.Background(), "udp", "127.0.0.1:0", &utpnet.Options{})
	if err != nil {
		t.Fatalf("listening on a uTP socket: %v", err)
	}
	t.Cleanup(func() { sock.Close() })

	cl.AddListener(sock)
	cl.AddDialer(torrent.NetworkDialer{Network: "utp", Dialer: sock})
	return cl, sock
}
