# integrated/socket_test.go

Every comment in `integrated/socket_test.go` at e56c9aa, verbatim.

## Line 28 — func numTransfers

```
// numTransfers is the concurrency this test runs at. It is overridable
// because the default does not fit everywhere: 1000 concurrent 1 MB transfers
// peaked at 7.7 GB RSS without the race detector when last measured (the whole
// package at 9.5 GB, enough for the kernel to kill it when other packages'
// tests run beside it on a 16 GB host), and the race detector's
// shadow memory puts that well past what a typical CI container allows. Set
// UTP_TEST_TRANSFERS to run the same test at a size that fits.
```

## Line 40 — func TestManyConcurrentTransfers

```
// NOTE: this test used to register an fgprof handler on
	// http.DefaultServeMux, start an HTTP server on a fixed port, and write
	// CPU/trace profiles into the source tree. TestManyTimeHugeData registered
	// the same mux pattern, so running the package's tests together panicked
	// with "multiple registrations for /debug/fgprof". Profiling belongs
	// behind `go test -cpuprofile`, not baked into the test.
```

## Line 47 — func TestManyConcurrentTransfers

```
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
```

## Line 58 — func TestManyConcurrentTransfers

```
//go func() {
	//	if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
	//		log.Error("Failure in running pprof server", "err", err)
	//	}
	//}()
```

## Line 64 — func TestManyConcurrentTransfers

```
// // 内存分析
	//memFile, _ := os.Create("concurrency_mem.prof")
	//defer func() {
	//	_ = pprof.WriteHeapProfile(memFile)
	//}()
```

## Line 70 — func TestManyConcurrentTransfers

```
// Initialize logging
	// Warnings and up, from this run only: a failed transfer prints its
	// connection's lines from both (logLinesFor).
```

## Line 76 — func TestManyConcurrentTransfers

```
// Closed before TempDir removes them -- cleanups run last first -- and
	// after the sockets, which the deferred Closes below shut first. Windows
	// will not delete a file that is open.
```

## Line 83 — func TestManyConcurrentTransfers

```
//handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: utp.LevelTrace})
```

## Line 86 — func TestManyConcurrentTransfers

```
//handler1 := log.NewTerminalHandler(logFile1, false)
	//handler2 := log.NewTerminalHandler(logFile2, false)
```

## Line 91 — func TestManyConcurrentTransfers

```
//logger1 := slog.Default()
	//logger2 := slog.Default()
```

## Line 100 — func TestManyConcurrentTransfers

```
// Setup addresses
```

## Line 104 — func TestManyConcurrentTransfers

```
//recvAddr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:3400")
	//sendAddr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:3401")
```

## Line 108 — func TestManyConcurrentTransfers

```
//recv, _, send, _ := buildConnectedPair()
	// Create sockets
	//recvLink := utp.WithSocket(ctx, recv, logger1.With("local", ":3400"))
```

## Line 117 — func TestManyConcurrentTransfers

```
//sendLink := utp.WithSocket(ctx, send, logger2.With("local", ":3401"))
```

## Line 123 — func TestManyConcurrentTransfers

```
// Create wait group for all transfers
```

## Line 130 — func TestManyConcurrentTransfers

```
// Start transfers
```

## Line 133 — func TestManyConcurrentTransfers

```
// One for sender, one for receiver
```

## Line 146 — func TestManyConcurrentTransfers

```
// The race detector costs roughly an order of magnitude here.
```

## Line 151 — func TestManyConcurrentTransfers

```
// Wait for all transfers in a separate goroutine
```

## Line 166 — func TestManyConcurrentTransfers

```
// A connection's event queue carries packets only until its event loop
	// is ready, and here a thousand set up at once. Anything it dropped would
	// be a loss of ours, not the network's (utp.connEventQueueLen).
```

## Line 174 — func TestUdpTransfer

```
// TestUdpTransfer performs a single large transfer over uTP carried on real
// UDP sockets -- the path a BitTorrent client uses.
//
// NOTE: this test previously did not touch the uTP library at all. It opened
// two net.UDPConns and blasted raw datagrams between them, and its send loop
// never advanced `offset` (the increment was commented out), so it looped
// forever and the test could only ever end at the timeout. It was a scratch
// benchmark of UDP loopback, not a test of this package. It has been rewritten
// to exercise what its name claims. See KNOWN-LIMITATIONS.md.
```

## Line 278 — func TestManyTimeHugeData

```
//cpuFile, _ := os.Create("cpu.prof")
	//pprof.StartCPUProfile(cpuFile)
	//defer pprof.StopCPUProfile()
```

## Line 287 — func OneHugeDataTransfer

```
// CPU 分析
	//cpuFile, _ := os.Create("cpu.prof")
	//pprof.StartCPUProfile(cpuFile)
	//defer pprof.StopCPUProfile()
```

## Line 292 — func OneHugeDataTransfer

```
// trace 分析
	//traceFile, _ := os.Create("trace.prof")
	//trace.Start(traceFile)
	//defer trace.Stop()
	// 内存分析
```

## Line 298 — func OneHugeDataTransfer

```
// 创建50MB的测试数据
	//hugeData := bytes.Repeat([]byte{0xf0}, 1024*1024*15)
```

## Line 309 — func OneHugeDataTransfer

```
//// Initialize logging
	//logFile1, _ := os.OpenFile(fmt.Sprintf("test_one_huge_data_transfer_%d.log", recvAddr.Port), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	//logFile2, _ := os.OpenFile(fmt.Sprintf("test_one_huge_data_transfer_%d.log", sendAddr.Port), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	////handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: utp.LevelTrace})
	//handler1 := log.NewTerminalHandler(logFile1, false)
	//handler2 := log.NewTerminalHandler(logFile2, false)
	//logger1 := slog.New(handler1)
	//logger2 := slog.New(handler2)
```

## Line 336 — func OneHugeDataTransfer

```
// 启动传输
```

## Line 341 — func OneHugeDataTransfer

```
// 等待接收完成
```

## Line 367 — func TestEmptySocketConnCount

```
// 假设我们有一个 UtpSocket 的包装结构
```

## Line 378 — func TestSocketReportsTwoConns

```
// create receive socket
```

## Line 383 — func TestSocketReportsTwoConns

```
// 创建发送socket
```

## Line 388 — func TestSocketReportsTwoConns

```
// 定义连接ID
```

## Line 414 — func TestSocketReportsTwoConns

```
// 启动第一对连接
```

## Line 425 — func TestSocketReportsTwoConns

```
// 启动第二对连接
```

## Line 464 — func initiateTransfer

```
// Start receiver goroutine
```

## Line 491 — func initiateTransfer

```
// Start sender goroutine
```

## Line 509 — func transferBudget

```
// transferBudget is the wall-clock allowance for a single large transfer.
```

## Line 517 — var transferLogs

```
// transferLogs are the files TestManyConcurrentTransfers logs to, receiver's
// first.
```

## Line 521 — func logLinesFor

```
// logLinesFor prints what both sockets logged about one transfer: the
// receiving connection's lines, which carry its ids as they are, and the
// sending one's, which carry them the other way round.
```
