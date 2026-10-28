package utp_test

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/arxodeus/utp"
)

func Example() {
	ctx := context.Background()
	loopback := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}

	server, err := utp.Bind(ctx, "udp", loopback, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()
	client, err := utp.Bind(ctx, "udp", loopback, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	accepted := make(chan *utp.UtpStream, 1)
	go func() {
		stream, err := server.Accept(ctx, nil)
		if err != nil {
			log.Fatal(err)
		}
		accepted <- stream
	}()

	peer := utp.NewUdpPeer(server.LocalAddr().(*net.UDPAddr))
	stream, err := client.Connect(ctx, peer, nil)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := stream.Write(ctx, []byte("hello")); err != nil {
		log.Fatal(err)
	}
	stream.Close()

	var got []byte
	in := <-accepted
	if _, err := in.ReadToEOF(ctx, &got); err != nil {
		log.Fatal(err)
	}
	in.Close()
	fmt.Println(string(got))
	// Output: hello
}
