package utpnet_test

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/arxodeus/utp/utpnet"
)

func Example() {
	ctx := context.Background()

	server, err := utpnet.Listen(ctx, "udp", "127.0.0.1:0", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()

	go func() {
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn) // echo
	}()

	client, err := utpnet.Listen(ctx, "udp", "127.0.0.1:0", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello")); err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(buf))
	// Output: hello
}
