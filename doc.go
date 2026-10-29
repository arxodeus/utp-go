// Package utp implements the Micro Transport Protocol (uTP, BEP 29), a
// reliable, ordered byte stream over UDP with LEDBAT delay-based congestion
// control, in pure Go.
//
// A Socket runs on one UDP socket (Bind) or any Conn (NewSocket) and
// carries any number of connections. Connect dials a peer and Accept waits
// for an incoming connection; both return a Stream, whose Read and Write
// take a context that governs that call:
//
//	sock, err := utp.Bind(ctx, "udp", laddr, nil)
//	if err != nil {
//		return err
//	}
//	defer sock.Close()
//
//	stream, err := sock.Connect(ctx, utp.NewUDPPeer(raddr), utp.NewConnectionConfig())
//	if err != nil {
//		return err
//	}
//	defer stream.Close()
//	_, err = stream.Write(ctx, []byte("hello"))
//
// For net.Conn and net.PacketConn, including sharing the UDP port with
// another protocol such as a DHT, use the utpnet package.
//
// # Congestion control
//
// Classic LEDBAT (RFC 6817) is the default. LEDBAT++ and RACK loss detection
// (RFC 8985) can be selected per connection in ConnectionConfig; both are
// experimental. See AlgorithmLEDBATPP for LEDBAT++'s known limitation.
//
// # Path MTU
//
// Each connection searches for the largest datagram its path carries,
// starting from ConnectionConfig.MaxPacketSize. Probes are sent with
// fragmentation disabled where the platform supports it, and ICMP reports can
// be fed in with Socket.ProcessICMPFragmentation and ProcessICMPError.
//
// # Logging
//
// Bind and NewSocket take a *slog.Logger; nil discards logging. Per-packet
// events are logged at LevelTrace.
package utp
