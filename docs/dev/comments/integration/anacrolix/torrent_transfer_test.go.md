# integration/anacrolix/torrent_transfer_test.go

Every comment in `integration/anacrolix/torrent_transfer_test.go` at e56c9aa, verbatim.

## Line 21 — func TestTorrentTransferOverUtp

```
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
```

## Line 45 — func transferTorrent

```
// transferTorrent moves a generated 4 MiB torrent from a seeding client to a
// leeching one, each made by newClient, and checks what arrived against the
// torrent's own piece hashes and the payload's.
```

## Line 51 — func transferTorrent

```
// 4 MiB, 128 pieces
```

## Line 59 — func transferTorrent

```
// Deterministic contents: a failure is reproducible, and the file is not
	// compressible enough for any layer to accidentally shorten it.
```

## Line 91 — func transferTorrent

```
// The seeder has the data already; make it say so before anyone asks.
```

## Line 107 — func transferTorrent

```
// Completion is two conditions, not one. `BytesCompleted == Length` alone
	// is true for a moment right after the torrent is added, before the
	// leecher has worked out that it has none of the pieces -- three runs in
	// five ended there, "complete" in 240ms with no file on disk. Requiring
	// that the whole payload also arrived as peer data is what makes this a
	// measurement of a transfer rather than of a race.
```

## Line 133 — func transferTorrent

```
// Read it back through the client rather than off the disk.
	//
	// The default storage writes to "<name>.part" and renames only once the
	// whole torrent is complete, which happens after the last piece is marked
	// complete rather than with it -- so reading the final path here found no
	// file in four runs out of five, on a transfer that had in fact finished.
	// The reader is what a consumer of the library uses, and it returns what
	// the client will serve: pieces that passed the metainfo's own hashes.
```

## Line 155 — func transferTorrent

```
// And the same bytes on disk, once the storage has renamed its part file.
	// Bounded, and not the assertion this test turns on: what matters is that
	// the transfer completed and verified, not how quickly anacrolix renames.
```

## Line 180 — func newUtpOnlyClient

```
// newUtpOnlyClient builds a torrent.Client that can reach the network only
// through this library.
//
// Every built-in listener is disabled -- TCP, torrent's own uTP, the DHT --
// so torrent creates no sockets of its own and `listenAll` is asked for no
// networks. The socket added afterwards is a *utpnet.Socket, used both as the
// listener torrent accepts on and, wrapped in a NetworkDialer, as the dialer
// it connects out with. If this library did not work, there would be no
// transport at all rather than a quiet fallback to TCP.
```
