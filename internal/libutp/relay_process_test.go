//go:build cgo

package libutp_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// A relay in its own process.
//
// The relay spins for the last stretch before each datagram is due
// (relaySpin), and in this test binary it spins in the same Go runtime as
// this library's receiver, while libutp's receiver is a thread of its own.
// That was expected to slow our receiver. It did the opposite: a runtime
// with a goroutine running wakes the socket's reader sooner than an idle
// one, so the harness flattered our receiver, and only ours. At 10 Mb/s its
// median turnaround was 80 us with the relay in the process and is 90 us
// without it, against libutp's 50 either way; its 90th percentile was 240 us
// and is 1.1 ms, libutp's. Running the relay as a separate process takes it
// out of the runtime altogether, and leaves both receivers sharing only the
// machine.
//
// The child is this test binary run again, with relayChildEnv set to its
// configuration. It reports its ports, answers requests for counts, and on
// close hands back what it recorded -- every datagram it delivered and every
// one offered to it, both directions, timed on its own clock as before.

// relayInOwnProcess makes newRelay start its relay in a child process. Set
// UTP_RELAY_PROCESS=1 to run every relay that way.
var relayInOwnProcess = os.Getenv("UTP_RELAY_PROCESS") == "1"

// relayChildEnv carries a child relay's configuration.
const relayChildEnv = "UTP_RELAY_CHILD"

type relayChildConfig struct {
	LibPort, GoPort uint16
	ToGo, ToLib     pathConfig
	Seed            int64
}

// relayPathDump is what the child recorded for one direction.
type relayPathDump struct {
	Stats     pathStats
	Delivered []relayEvent
	Trace     []relayEvent
}

type relayDump struct {
	ToGo, ToLib relayPathDump
}

func TestMain(m *testing.M) {
	if cfg := os.Getenv(relayChildEnv); cfg != "" {
		if err := runRelayChild(cfg, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "relay child:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runRelayChild is the child's side: start the relay, say where it listens,
// then serve "stats" and "close" from in until close or end of input.
func runRelayChild(cfgJSON string, in io.Reader, out io.Writer) error {
	var cfg relayChildConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return err
	}
	var dump relayDump
	r, err := startRelay(cfg.LibPort, cfg.GoPort, cfg.ToGo, cfg.ToLib, cfg.Seed, func(r *relay) {
		r.toGo.delivered, r.toGo.trace = &dump.ToGo.Delivered, &dump.ToGo.Trace
		r.toLib.delivered, r.toLib.trace = &dump.ToLib.Delivered, &dump.ToLib.Trace
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	if err := enc.Encode([2]uint16{r.LibutpFacingPort(), uint16(r.GoFacingAddr().Port)}); err != nil {
		return err
	}
	lines := bufio.NewScanner(in)
	for lines.Scan() {
		switch strings.TrimSpace(lines.Text()) {
		case "stats":
			if err := enc.Encode([2]pathStats{r.toGo.Stats(), r.toLib.Stats()}); err != nil {
				return err
			}
		case "close":
			r.Close()
			dump.ToGo.Stats, dump.ToLib.Stats = r.toGo.Stats(), r.toLib.Stats()
			return enc.Encode(&dump)
		}
	}
	// The parent went away without closing.
	r.Close()
	return lines.Err()
}

// relayChild is the parent's handle on a relay in its own process.
type relayChild struct {
	r            *relay
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	dec          *json.Decoder
	portA, portB uint16

	mu     sync.Mutex
	closed bool
	final  relayDump
}

// startRelayProcess is newRelay in a child process.
//
// onNewRelay sees the relay here, before the child starts, and the child is
// given the paths' configuration as the hook leaves it: hooks set a path's
// impairment that way (libutpSendsTo's return path does), and a child started
// first ran with the configuration the hook was meant to replace.
func startRelayProcess(libPort, goPort uint16, toGo, toLib pathConfig, seed int64) (*relay, error) {
	c := &relayChild{}
	r := &relay{child: c,
		toGo:  &relayPath{cfg: toGo, child: c, name: "toGo"},
		toLib: &relayPath{cfg: toLib, child: c, name: "toLib"}}
	c.r = r
	if onNewRelay != nil {
		onNewRelay(r)
	}
	cfg, err := json.Marshal(relayChildConfig{LibPort: libPort, GoPort: goPort,
		ToGo: r.toGo.cfg, ToLib: r.toLib.cfg, Seed: seed})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), relayChildEnv+"="+string(cfg))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.cmd, c.stdin, c.dec = cmd, stdin, json.NewDecoder(stdout)
	var ports [2]uint16
	if err := c.dec.Decode(&ports); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("relay child did not start: %w", err)
	}
	c.portA, c.portB = ports[0], ports[1]
	return r, nil
}

// stats asks the child for one direction's counts, or reports the last ones
// once it has closed.
func (c *relayChild) stats(name string) pathStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	pick := func(toGo, toLib pathStats) pathStats {
		if name == "toGo" {
			return toGo
		}
		return toLib
	}
	if c.closed {
		return pick(c.final.ToGo.Stats, c.final.ToLib.Stats)
	}
	var s [2]pathStats
	if _, err := io.WriteString(c.stdin, "stats\n"); err != nil {
		return pathStats{}
	}
	if err := c.dec.Decode(&s); err != nil {
		return pathStats{}
	}
	return pick(s[0], s[1])
}

// close closes the child's relay and takes what it recorded into whatever
// the paths here were given to record into.
func (c *relayChild) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if _, err := io.WriteString(c.stdin, "close\n"); err == nil {
		if err := c.dec.Decode(&c.final); err != nil {
			fmt.Fprintln(os.Stderr, "relay child: no record on close:", err)
		}
	}
	_ = c.stdin.Close()
	_ = c.cmd.Wait()
	for _, p := range []struct {
		path *relayPath
		dump relayPathDump
	}{{c.r.toGo, c.final.ToGo}, {c.r.toLib, c.final.ToLib}} {
		if p.path.delivered != nil {
			*p.path.delivered = append(*p.path.delivered, p.dump.Delivered...)
		}
		if p.path.trace != nil {
			*p.path.trace = append(*p.path.trace, p.dump.Trace...)
		}
	}
}

// A path impairment set by onNewRelay reaches a relay in its own process.
// The child once started before the hook ran and kept the configuration the
// hook replaced: TestAsymmetricAckPath's 64 kb/s return path then carried 20
// Mb/s, and libutp's receiver, which takes 7.3 s over the narrow path, took
// 2.0.
func TestRelayChildTakesHookedConfig(t *testing.T) {
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	port := uint16(sink.LocalAddr().(*net.UDPAddr).Port)

	defer func(was bool) { relayInOwnProcess = was }(relayInOwnProcess)
	relayInOwnProcess = true
	onNewRelay = func(r *relay) { r.toLib.cfg = pathConfig{BandwidthBps: 64_000, QueueBytes: 8 * 1024} }
	r := newRelay(t, port, port, pathConfig{}, pathConfig{}, 1)
	onNewRelay = nil
	defer r.Close()

	src, err := net.DialUDP("udp4", nil, r.GoFacingAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	b := make([]byte, 20)
	for i := 0; i < 2000; i++ {
		_, _ = src.Write(b)
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.toLib.Stats().Offered < 2000 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// 8 KB of 20-byte datagrams is about 400; everything past it is dropped.
	if s := r.toLib.Stats(); s.DroppedByQueue < 1000 {
		t.Fatalf("the return path dropped %d of %d offered at 64 kb/s with an 8 KB queue: the "+
			"hook's configuration did not reach the child (%+v)", s.DroppedByQueue, s.Offered, s)
	}
}
