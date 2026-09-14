package netem

import (
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// ConnectionMetrics.QueueingDelay must measure a queue, on the path this
// sender is sending into. An asymmetric path is what tells the difference.
//
// The two directions here are given very different propagation delays and no
// bottleneck at all, so neither direction has a standing queue: the right
// answer is approximately zero, whatever the delays are.
//
// It used to be the difference between the two directions. BaseDelay comes
// from the peer's measurement of our *outbound* path (libutp's `our_hist`,
// utp_internal.cpp:2016-2021) and PeerTsDiff is our own measurement of the
// *inbound* one (`their_delay`, :2000-2001); subtracting one from the other
// gives the path asymmetry, with any real queue buried inside it. On this
// link that produced about 90ms of queue on a path with none.
//
// The congestion controller was never affected -- it compares a sample
// against the base of the same series, which it has in hand. What this fed
// was the queueing delay Recorder reports.
func TestQueueingDelayMeasuresTheOutboundQueue(t *testing.T) {
	// One direction slow, the other fast, and nothing to queue behind.
	const outbound = 10 * time.Millisecond
	const inbound = 100 * time.Millisecond

	n := NewNetwork(77)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	n.ConnectAsymmetric(sender, receiver,
		Config{Delay: outbound, BandwidthBps: 50_000_000, QueueBytes: 256 * 1024},
		Config{Delay: inbound, BandwidthBps: 50_000_000, QueueBytes: 256 * 1024},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sendSock := utp.WithSocket(ctx, sender, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, receiver, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(sender.Addr(), 861, 860)
	connectCid := utp.NewConnectionId(receiver.Addr(), 860, 861)

	var (
		mu       sync.Mutex
		samples  []utp.ConnectionMetrics
		maxQueue time.Duration
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		if m.BaseDelay <= 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		samples = append(samples, m)
		if q := m.QueueingDelay(); q > maxQueue {
			maxQueue = q
		}
	}

	// Small and slow enough that the link is never near its capacity: the
	// point is a path with no queue, so any reported queue is an artefact.
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		if _, err := stream.ReadToEOF(ctx, &buf); err != nil {
			t.Errorf("read: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			t.Errorf("connect: %v", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(samples) == 0 {
		t.Fatal("no metrics samples with a base delay; the test observed nothing")
	}

	// What the old formula would have produced, computed from the same
	// samples, so the comparison is not a claim about a previous run.
	var maxCrossDirection time.Duration
	for _, m := range samples {
		if m.PeerTsDiff > m.BaseDelay {
			if q := m.PeerTsDiff - m.BaseDelay; q > maxCrossDirection {
				maxCrossDirection = q
			}
		}
	}

	t.Logf("%d samples; outbound %v inbound %v; queueing delay max %v; "+
		"cross-direction difference max %v",
		len(samples), outbound, inbound, maxQueue, maxCrossDirection)

	// The path asymmetry is 90ms. A queue reported anywhere near it is the
	// asymmetry, not a queue.
	const asymmetry = inbound - outbound
	if maxQueue > asymmetry/2 {
		t.Errorf("queueing delay reached %v on a path with no bottleneck and a %v "+
			"asymmetry; it is reporting the asymmetry", maxQueue, asymmetry)
	}

	// And the control: the old formula really does report the asymmetry on
	// this link, so the assertion above is not passing for some other reason.
	if maxCrossDirection < asymmetry/2 {
		t.Errorf("the cross-direction difference only reached %v, so this link does "+
			"not expose the defect and the test proves nothing", maxCrossDirection)
	}
}

// The other half: the metric must still see a queue that is really there.
//
// The test above only proves it stops reporting a queue that is not. A metric
// hardwired to zero would pass that one, so this puts a real bottleneck under
// it and checks the reported queue against the emulated link's own
// measurement -- taken at the bottleneck, independently of anything the
// connection believes.
func TestQueueingDelayTracksARealQueue(t *testing.T) {
	n := NewNetwork(78)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	// A narrow bottleneck with a deep queue: LEDBAT will fill it to its
	// target and hold there, which is a standing queue by construction.
	n.Connect(sender, receiver, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 4_000_000,
		QueueBytes:   512 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sendSock := utp.WithSocket(ctx, sender, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, receiver, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(sender.Addr(), 871, 870)
	connectCid := utp.NewConnectionId(receiver.Addr(), 870, 871)

	var (
		mu      sync.Mutex
		sum     time.Duration
		count   int
		maxSeen time.Duration
	)
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 5 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		if m.BaseDelay <= 0 {
			return
		}
		q := m.QueueingDelay()
		mu.Lock()
		defer mu.Unlock()
		sum += q
		count++
		if q > maxSeen {
			maxSeen = q
		}
	}

	payload := make([]byte, 2*1024*1024)
	for i := range payload {
		payload[i] = byte(i * 7)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		if _, err := stream.ReadToEOF(ctx, &buf); err != nil {
			t.Errorf("read: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			t.Errorf("connect: %v", err)
			return
		}
		defer stream.Close()
		if _, err := stream.Write(ctx, payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	wg.Wait()

	linkQueue := n.Link("sender", "receiver").Stats().MeanQueueDelay()

	mu.Lock()
	defer mu.Unlock()
	if count == 0 {
		t.Fatal("no metrics samples with a base delay; the test observed nothing")
	}
	reported := sum / time.Duration(count)
	t.Logf("%d samples; reported queueing delay mean %v max %v; link measured mean %v",
		count, reported.Round(time.Microsecond), maxSeen.Round(time.Microsecond),
		linkQueue.Round(time.Microsecond))

	if linkQueue < 5*time.Millisecond {
		t.Fatalf("the link only queued %v, so there is no queue to detect and this "+
			"test proves nothing", linkQueue)
	}
	if reported == 0 {
		t.Fatal("the metric reported no queue at all while the link queued " +
			linkQueue.String())
	}
	// Not equality: the link's figure is the mean over every packet it
	// carried, and the metric is sampled from the sender's timestamps at a
	// different cadence. Within a factor of three is enough to show it is
	// measuring the same thing rather than something unrelated.
	if reported*3 < linkQueue || linkQueue*3 < reported {
		t.Errorf("reported queueing delay %v against the link's %v: more than a "+
			"factor of three apart", reported, linkQueue)
	}
}
