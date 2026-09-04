package integrated

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utp "github.com/zen-eth/utp-go"
)

const (
	test_socket_data_len = 1_000_000
	numTransfers         = 1000
)

func TestManyConcurrentTransfers(t *testing.T) {
	// NOTE: this test used to register an fgprof handler on
	// http.DefaultServeMux, start an HTTP server on a fixed port, and write
	// CPU/trace profiles into the source tree. TestManyTimeHugeData registered
	// the same mux pattern, so running the package's tests together panicked
	// with "multiple registrations for /debug/fgprof". Profiling belongs
	// behind `go test -cpuprofile`, not baked into the test.

	// profile name: allocs
	// profile name: block
	// profile name: goroutine
	// profile name: heap
	// profile name: mutex
	// profile name: threadcreate
	//goFile, _ := os.Create("concurrency_goroutine.prof")
	//goProfile := pprof.Lookup("goroutine")
	//go goProfile.WriteTo(goFile, 0)
	//defer goFile.Close()

	//go func() {
	//	if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
	//		log.Error("Failure in running pprof server", "err", err)
	//	}
	//}()

	// // 内存分析
	//memFile, _ := os.Create("concurrency_mem.prof")
	//defer func() {
	//	_ = pprof.WriteHeapProfile(memFile)
	//}()

	// Initialize logging
	logFile1, _ := os.OpenFile("test_socket_many_concurrent_transfer_3400.log", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	logFile2, _ := os.OpenFile("test_socket_many_concurrent_transfer_3401.log", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	//handler := log.NewTerminalHandler(os.Stdout, true)
	handler1 := log.NewTerminalHandlerWithLevel(logFile1, log.LevelError, false)
	handler2 := log.NewTerminalHandlerWithLevel(logFile2, log.LevelError, false)
	//handler1 := log.NewTerminalHandler(logFile1, false)
	//handler2 := log.NewTerminalHandler(logFile2, false)
	logger1 := log.NewLogger(handler1)
	logger2 := log.NewLogger(handler2)

	//logger1 := log.Root()
	//logger2 := log.Root()
	t.Logf("opened trace: %v", logger2.Enabled(nil, log.LevelTrace))
	t.Logf("opened info: %v", logger2.Enabled(nil, log.LevelInfo))
	t.Logf("opened warn: %v", logger2.Enabled(nil, log.LevelWarn))
	t.Logf("opened error: %v", logger2.Enabled(nil, log.LevelError))

	t.Log("starting socket test")

	// Setup addresses
	recvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3400}
	sendAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3401}

	//recvAddr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:3400")
	//sendAddr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:3401")

	ctx := context.Background()
	//recv, _, send, _ := buildConnectedPair()
	// Create sockets
	//recvLink := utp.WithSocket(ctx, recv, logger1.New("local", ":3400"))
	recvLink, err := utp.Bind(ctx, "udp4", recvAddr, logger1.New("local", ":3400"))
	if err != nil {
		t.Fatalf("Failed to bind recv socket: %v", err)
	}
	defer recvLink.Close()

	//sendLink := utp.WithSocket(ctx, send, logger2.New("local", ":3401"))
	sendLink, err := utp.Bind(ctx, "udp4", sendAddr, logger2.New("local", ":3401"))
	if err != nil {
		t.Fatalf("Failed to bind send socket: %v", err)
	}
	defer sendLink.Close()
	// Create wait group for all transfers
	var wg sync.WaitGroup
	start := time.Now()

	data := make([]byte, test_socket_data_len)
	_, _ = io.ReadFull(rand.Reader, data)

	// Start transfers
	for i := 0; i < numTransfers; i++ {
		wg.Add(2) // One for sender, one for receiver
		initiateTransfer(
			t, ctx,
			uint16(i*2),
			recvAddr,
			recvLink,
			sendAddr,
			sendLink,
			data,
			&wg)
	}
	budget := 2 * time.Minute
	if raceEnabled {
		// The race detector costs roughly an order of magnitude here.
		budget = 10 * time.Minute
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, budget)
	go func() {
		// Wait for all transfers in a separate goroutine
		wg.Wait()
		cancel()
	}()

	<-timeoutCtx.Done()
	require.ErrorIs(t, timeoutCtx.Err(), context.Canceled, "Timed out waiting for transfer to complete")

	elapsed := time.Since(start)
	megabitsSent := float64(numTransfers) * float64(test_socket_data_len) * 8.0 / 1_000_000.0
	transferRate := megabitsSent / elapsed.Seconds()

	t.Logf("finished high concurrency load test of %d simultaneous transfers, in %v, at a rate of %.0f Mbps",
		numTransfers, elapsed, transferRate)
}

// TestUdpTransfer performs a single large transfer over uTP carried on real
// UDP sockets -- the path a BitTorrent client uses.
//
// NOTE: this test previously did not touch the uTP library at all. It opened
// two net.UDPConns and blasted raw datagrams between them, and its send loop
// never advanced `offset` (the increment was commented out), so it looped
// forever and the test could only ever end at the timeout. It was a scratch
// benchmark of UDP loopback, not a test of this package. It has been rewritten
// to exercise what its name claims. See KNOWN-LIMITATIONS.md.
func TestUdpTransfer(t *testing.T) {
	handler := log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false)
	logger := log.NewLogger(handler)

	recvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3600}
	sendAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3601}

	ctx := context.Background()
	recvLink, err := utp.Bind(ctx, "udp4", recvAddr, logger)
	require.NoError(t, err, "Failed to bind recv socket")
	defer recvLink.Close()
	sendLink, err := utp.Bind(ctx, "udp4", sendAddr, logger)
	require.NoError(t, err, "Failed to bind send socket")
	defer sendLink.Close()

	hugeData := make([]byte, 16*1024*1024)
	_, err = io.ReadFull(rand.Reader, hugeData)
	require.NoError(t, err)
	length := len(hugeData)

	connConfig := utp.NewConnectionConfig()
	const initiatorCid, responderCid = 3700, 3701
	recvCid := utp.NewConnectionId(utp.NewUdpPeer(sendAddr), responderCid, initiatorCid)
	sendCid := utp.NewConnectionId(utp.NewUdpPeer(recvAddr), initiatorCid, responderCid)

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)

	var recvErr error
	var recvBuf []byte
	go func() {
		defer wg.Done()
		stream, err := recvLink.AcceptWithCid(ctx, recvCid, connConfig)
		if err != nil {
			recvErr = fmt.Errorf("accept: %w", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, length)
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			recvErr = fmt.Errorf("read: %w", err)
			return
		}
		if n != length {
			recvErr = fmt.Errorf("short read: got %d want %d", n, length)
			return
		}
		recvBuf = buf
	}()

	var sendErr error
	go func() {
		defer wg.Done()
		stream, err := sendLink.ConnectWithCid(ctx, sendCid, connConfig)
		if err != nil {
			sendErr = fmt.Errorf("connect: %w", err)
			return
		}
		defer stream.Close()
		n, err := stream.Write(ctx, hugeData)
		if err != nil {
			sendErr = fmt.Errorf("write: %w", err)
			return
		}
		if n != length {
			sendErr = fmt.Errorf("short write: got %d want %d", n, length)
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(transferBudget()):
		t.Fatalf("transfer did not complete within %v", transferBudget())
	}

	require.NoError(t, sendErr)
	require.NoError(t, recvErr)
	require.True(t, bytes.Equal(hugeData, recvBuf), "received data does not match sent data")

	elapsed := time.Since(start)
	megabytesSent := float64(length) / 1_000_000.0
	transferRate := megabytesSent * 8.0 / elapsed.Seconds()
	t.Logf(
		"finished single large transfer test with %.0f MB, in %v, at a rate of %.1f Mbps (loopback)",
		megabytesSent,
		elapsed,
		transferRate,
	)
}

func TestManyTimeHugeData(t *testing.T) {
	//cpuFile, _ := os.Create("cpu.prof")
	//pprof.StartCPUProfile(cpuFile)
	//defer pprof.StopCPUProfile()
	for i := 0; i < 1; i++ {
		OneHugeDataTransfer(t, i*2)
	}
}

func OneHugeDataTransfer(t *testing.T, i int) {
	// CPU 分析
	//cpuFile, _ := os.Create("cpu.prof")
	//pprof.StartCPUProfile(cpuFile)
	//defer pprof.StopCPUProfile()

	// trace 分析
	//traceFile, _ := os.Create("trace.prof")
	//trace.Start(traceFile)
	//defer trace.Stop()
	// 内存分析

	// 创建50MB的测试数据
	//hugeData := bytes.Repeat([]byte{0xf0}, 1024*1024*15)
	hugeData := make([]byte, 1024*1024*50)

	_, _ = io.ReadFull(rand.Reader, hugeData)

	t.Log("starting single transfer of huge data test")

	recvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3500 + i}
	sendAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3501 + i}

	//// Initialize logging
	//logFile1, _ := os.OpenFile(fmt.Sprintf("test_one_huge_data_transfer_%d.log", recvAddr.Port), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	//logFile2, _ := os.OpenFile(fmt.Sprintf("test_one_huge_data_transfer_%d.log", sendAddr.Port), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	////handler := log.NewTerminalHandler(os.Stdout, true)
	//handler1 := log.NewTerminalHandler(logFile1, false)
	//handler2 := log.NewTerminalHandler(logFile2, false)
	//logger1 := log.NewLogger(handler1)
	//logger2 := log.NewLogger(handler2)

	logger1 := log.Root()
	logger2 := log.Root()

	ctx := context.Background()
	recv, err := utp.Bind(ctx, "udp", recvAddr, logger1.New("local", fmt.Sprintf(":%d", recvAddr.Port)))
	require.NoError(t, err, "Failed to bind recv socket")
	send, err := utp.Bind(ctx, "udp", sendAddr, logger2.New("local", fmt.Sprintf(":%d", sendAddr.Port)))
	require.NoError(t, err, "Failed to bind send socket")

	defer func() {
		recv.Close()
		send.Close()
	}()
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(2)

	// 启动传输
	go func() {
		initiateTransfer(t, ctx, uint16(2), recvAddr, recv, sendAddr, send, hugeData, &wg)
	}()

	// 等待接收完成
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Second)
	go func() {
		wg.Wait()
		cancel()
	}()

	<-timeoutCtx.Done()
	require.ErrorIs(t, timeoutCtx.Err(), context.Canceled, "Timed out waiting for transfer to complete")

	elapsed := time.Since(start)
	megabytesSent := float64(len(hugeData)) / 1_000_000.0
	megabitsSent := megabytesSent * 8.0
	transferRate := megabitsSent / elapsed.Seconds()

	t.Logf(
		"finished single large transfer test with %.0f MB, in %v, at a rate of %.1f Mbps",
		megabytesSent,
		elapsed,
		transferRate,
	)
}

func TestEmptySocketConnCount(t *testing.T) {
	socketAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3402}

	// 假设我们有一个 UtpSocket 的包装结构
	utpSocket, err := utp.Bind(context.Background(), "udp", socketAddr, nil)
	require.NoError(t, err, "Failed to bind socket")
	defer utpSocket.Close()

	require.Equal(t, 0, utpSocket.NumConnections(), "Expected 0 connections, got %d", utpSocket.NumConnections())
}

func TestSocketReportsTwoConns(t *testing.T) {
	ctx := context.Background()
	connConfig := utp.NewConnectionConfig()
	// create receive socket
	recvAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3404}
	recv, err := utp.Bind(ctx, "udp", recvAddr, nil)
	require.NoError(t, err, "Failed to bind recv socket")

	// 创建发送socket
	sendAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3405}
	send, err := utp.Bind(ctx, "udp", sendAddr, nil)
	require.NoError(t, err, "Failed to bind send socket")

	// 定义连接ID
	recvOneCid := &utp.ConnectionId{
		Send: 100,
		Recv: 101,
		Peer: utp.NewUdpPeer(sendAddr),
	}
	sendOneCid := &utp.ConnectionId{
		Send: 101,
		Recv: 100,
		Peer: utp.NewUdpPeer(recvAddr),
	}

	recvTwoCid := &utp.ConnectionId{
		Send: 200,
		Recv: 201,
		Peer: utp.NewUdpPeer(sendAddr),
	}
	sendTwoCid := &utp.ConnectionId{
		Send: 201,
		Recv: 200,
		Peer: utp.NewUdpPeer(recvAddr),
	}

	var wg sync.WaitGroup
	wg.Add(4)

	// 启动第一对连接
	go func() {
		defer wg.Done()
		_, _ = recv.AcceptWithCid(ctx, recvOneCid, connConfig)
	}()

	go func() {
		defer wg.Done()
		_, _ = send.ConnectWithCid(ctx, sendOneCid, connConfig)
	}()

	// 启动第二对连接
	go func() {
		defer wg.Done()
		_, _ = recv.AcceptWithCid(ctx, recvTwoCid, connConfig)
	}()

	go func() {
		defer wg.Done()
		_, _ = send.ConnectWithCid(ctx, sendTwoCid, connConfig)
	}()

	wg.Wait()

	if recv.NumConnections() != 2 {
		t.Errorf("Expected 2 connections for receiver, got %d", recv.NumConnections())
	}
	if send.NumConnections() != 2 {
		t.Errorf("Expected 2 connections for sender, got %d", send.NumConnections())
	}
}

func initiateTransfer(
	t *testing.T,
	ctx context.Context,
	i uint16,
	recvAddr *net.UDPAddr,
	recv *utp.UtpSocket,
	sendAddr *net.UDPAddr,
	send *utp.UtpSocket,
	data []byte,
	wg *sync.WaitGroup) {
	connConfig := utp.NewConnectionConfig()
	initiatorCid := uint16(100) + i
	responderCid := uint16(100) + i + 1

	recvCid := utp.NewConnectionId(utp.NewUdpPeer(sendAddr), responderCid, initiatorCid)

	sendCid := utp.NewConnectionId(utp.NewUdpPeer(recvAddr), initiatorCid, responderCid)

	// Start receiver goroutine
	go func() {
		defer func() {
			t.Log("recv wg done")
			wg.Done()
		}()
		stream, err := recv.AcceptWithCid(ctx, recvCid, connConfig)
		if err != nil {
			panic(fmt.Errorf("accept failed: %w", err))
		}

		buf := make([]byte, 0)
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			assert.NoError(t, err, "CID send=%d recv=%d read to eof error: %v",
				recvCid.Send, recvCid.Recv, err)
		}
		t.Logf("CID send=%d recv=%d read %d bytes from uTP stream",
			recvCid.Send, recvCid.Recv, n)
		assert.Equal(t, len(data), n,
			"received wrong number of bytes: got %d, want %d", n, len(data))
		assert.True(t, bytes.Equal(data, buf),
			"received data doesn't match sent data")
		stream.Close()
	}()

	// Start sender goroutine
	go func() {
		defer func() {
			t.Log("writer wg done")
			wg.Done()
		}()
		stream, err := send.ConnectWithCid(ctx, sendCid, connConfig)
		assert.NoError(t, err, "connect failed: %w", err)
		require.NotNil(t, stream, "stream should not be nil")
		n, err := stream.Write(ctx, data)
		assert.NoError(t, err, "write failed: %w", err)

		assert.Equal(t, len(data), n,
			"sent wrong number of bytes: got %d, want %d", n, len(data))
		stream.Close()
	}()
}

// transferBudget is the wall-clock allowance for a single large transfer.
func transferBudget() time.Duration {
	if raceEnabled {
		return 10 * time.Minute
	}
	return 120 * time.Second
}
