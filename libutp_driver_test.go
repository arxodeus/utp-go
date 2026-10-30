package utp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// refDriver is the reference implementation, libutp, as the conformance tests
// drive it: a deterministic machine with a virtual clock, a scripted random
// source, and packets in and out by explicit call.
//
// By default it replays a transcript recorded from real libutp, in
// testdata/libutp (see libutp_transcript_test.go). Each call must match the
// recorded one, and returns what libutp returned. A call that does not match
// means the behaviour the test compares has changed, and the test must be
// run against real libutp to decide whether it still agrees.
//
// With UTP_LIBUTP_DRIVER set to the path of the libutp-driver program
// (internal/integration/libutp/cmd/libutp-driver), it runs real libutp
// instead. With UTP_LIBUTP_RECORD=1 as well, it rewrites the transcripts of
// every test that ran.
type refDriver struct {
	t    testing.TB
	name string

	mu       sync.Mutex
	now      uint64
	diverged bool
	closed   bool

	// live mode
	proc *exec.Cmd
	in   *bufio.Writer
	out  *bufio.Scanner
	rec  *recordedDriver

	// replay mode
	replay *replayCursor
}

// refState mirrors libutp's connection states.
type refState int

const (
	refStateIdle refState = iota
	refStateConnecting
	refStateConnected
	refStateEOF
	refStateDestroyed
	refStateError
)

func liveDriverPath() string { return os.Getenv("UTP_LIBUTP_DRIVER") }
func recordingDrivers() bool { return liveDriverPath() != "" && os.Getenv("UTP_LIBUTP_RECORD") == "1" }

// newRefDriver starts a reference driver whose virtual clock starts at
// nowMicros.
func newRefDriver(t testing.TB, nowMicros uint64) (*refDriver, error) {
	t.Helper()
	name := t.Name()
	file := transcriptFile(name)

	transcripts.Lock()
	index := transcripts.created[t]
	transcripts.created[t]++
	stored, err := loadTranscripts(file)
	transcripts.Unlock()
	if err != nil {
		return nil, err
	}

	d := &refDriver{t: t, name: name, now: nowMicros}
	if path := liveDriverPath(); path != "" {
		if err := d.start(path, nowMicros); err != nil {
			return nil, err
		}
		if recordingDrivers() {
			d.rec = &recordedDriver{start: nowMicros}
			transcripts.Lock()
			tests := transcripts.recorded[file]
			if tests == nil {
				tests = map[string][]*recordedDriver{}
				transcripts.recorded[file] = tests
			}
			if index == 0 {
				tests[name] = nil
			}
			tests[name] = append(tests[name], d.rec)
			transcripts.Unlock()
		}
		return d, nil
	}

	drivers := stored.Tests[name]
	if index >= len(drivers) {
		if strings.HasPrefix(name, "Fuzz") {
			t.Skip("no recorded libutp transcript for this input; set UTP_LIBUTP_DRIVER to run it against libutp")
		}
		return nil, fmt.Errorf("no recorded libutp transcript for driver %d of %s in %s; record it with UTP_LIBUTP_DRIVER and UTP_LIBUTP_RECORD=1", index, name, file)
	}
	tr := drivers[index]
	if tr.Start != nowMicros {
		return nil, fmt.Errorf("%s: driver %d recorded starting at %d, now starting at %d", name, index, tr.Start, nowMicros)
	}
	d.replay = &replayCursor{file: stored, entries: tr.Entries}
	t.Cleanup(func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.diverged && !d.replay.done() {
			sc, _ := d.replay.next()
			t.Errorf("libutp transcript for %s: recorded calls were not made, from call %d, %s", name, d.replay.index-1, sc.Op)
		}
	})
	return d, nil
}

func (d *refDriver) start(path string, nowMicros uint64) error {
	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	d.proc = cmd
	d.in = bufio.NewWriter(stdin)
	d.out = bufio.NewScanner(stdout)
	d.out.Buffer(make([]byte, 1<<20), 64<<20)
	_, err = d.send(driverCall{Op: "new", U: nowMicros})
	return err
}

// send makes one call on the live driver.
func (d *refDriver) send(c driverCall) (driverCall, error) {
	line, err := json.Marshal(struct {
		Op string `json:"op"`
		U  uint64 `json:"u,omitempty"`
		B  []byte `json:"b,omitempty"`
	}{c.Op, c.U, c.B})
	if err != nil {
		return c, err
	}
	if _, err := d.in.Write(append(line, '\n')); err != nil {
		return c, err
	}
	if err := d.in.Flush(); err != nil {
		return c, err
	}
	if !d.out.Scan() {
		if err := d.out.Err(); err != nil {
			return c, err
		}
		return c, errors.New("libutp-driver exited")
	}
	var resp struct {
		I   int      `json:"i"`
		B   [][]byte `json:"b"`
		S   []string `json:"s"`
		Err string   `json:"err"`
	}
	if err := json.Unmarshal(d.out.Bytes(), &resp); err != nil {
		return c, err
	}
	c.RI, c.RB, c.RS, c.Err = resp.I, resp.B, resp.S, resp.Err
	return c, nil
}

// call makes c, against libutp or against the transcript, and returns it
// with libutp's results filled in.
func (d *refDriver) call(c driverCall) driverCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.diverged || d.closed {
		return c
	}
	if d.proc != nil {
		got, err := d.send(c)
		if err != nil {
			d.diverged = true
			d.t.Errorf("libutp-driver: %s: %v", c.Op, err)
			return c
		}
		if d.rec != nil {
			d.rec.calls = append(d.rec.calls, got)
		}
		return got
	}
	sc, ok := d.replay.next()
	if !ok {
		d.diverged = true
		d.t.Errorf("libutp transcript for %s: call %d, %s, was not recorded; the behaviour compared has changed, run against libutp to check it",
			d.name, d.replay.index, describeCall(c))
		return c
	}
	if sc.Op != c.Op || sc.U != c.U || sc.Arg != encodeArg(c.B) {
		d.diverged = true
		d.t.Errorf("libutp transcript for %s: call %d is %s, recorded %s(u=%d, arg=%s); the behaviour compared has changed, run against libutp to check it",
			d.name, d.replay.index-1, describeCall(c), sc.Op, sc.U, sc.Arg)
		return c
	}
	got, err := d.replay.result(sc, c)
	if err != nil {
		d.diverged = true
		d.t.Errorf("libutp transcript for %s: call %d: %v", d.name, d.replay.index-1, err)
	}
	return got
}

func describeCall(c driverCall) string {
	switch {
	case c.B != nil:
		return fmt.Sprintf("%s(%x)", c.Op, c.B)
	case c.U != 0:
		return fmt.Sprintf("%s(%d)", c.Op, c.U)
	}
	return c.Op + "()"
}

func (d *refDriver) PushRandom(v uint32)  { d.call(driverCall{Op: "push_random", U: uint64(v)}) }
func (d *refDriver) SetUDPMTU(mtu uint16) { d.call(driverCall{Op: "set_udp_mtu", U: uint64(mtu)}) }
func (d *refDriver) EnableCCLog()         { d.call(driverCall{Op: "enable_cc_log"}) }
func (d *refDriver) CCLog() []string      { return d.call(driverCall{Op: "cc_log"}).RS }
func (d *refDriver) Listen()              { d.call(driverCall{Op: "listen"}) }
func (d *refDriver) CheckTimeouts()       { d.call(driverCall{Op: "check_timeouts"}) }
func (d *refDriver) IssueAcks()           { d.call(driverCall{Op: "issue_acks"}) }
func (d *refDriver) Emitted() [][]byte    { return d.call(driverCall{Op: "emitted"}).RB }
func (d *refDriver) ClearEmitted()        { d.call(driverCall{Op: "clear_emitted"}) }
func (d *refDriver) CloseStream()         { d.call(driverCall{Op: "close_stream"}) }
func (d *refDriver) State() refState      { return refState(d.call(driverCall{Op: "state"}).RI) }

// Inject feeds one packet in, as if from the peer. It returns 1 if libutp
// claimed it.
func (d *refDriver) Inject(pkt []byte) int {
	if pkt == nil {
		pkt = []byte{}
	}
	return d.call(driverCall{Op: "inject", B: bytes.Clone(pkt)}).RI
}

// Write queues application data for libutp to send.
func (d *refDriver) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	c := d.call(driverCall{Op: "write", B: bytes.Clone(b)})
	if c.Err != "" {
		return c.RI, errors.New(c.Err)
	}
	return c.RI, nil
}

// Connect starts an outgoing connection, emitting a SYN.
func (d *refDriver) Connect() error {
	if c := d.call(driverCall{Op: "connect"}); c.Err != "" {
		return errors.New(c.Err)
	}
	return nil
}

// SetTime and Advance move the virtual clock, in microseconds. Now reads it,
// from any goroutine: a connection given the driver's clock reads it from its
// own goroutines, at times no transcript could record, so the clock is kept
// here rather than asked of libutp.
func (d *refDriver) SetTime(micros uint64) {
	d.call(driverCall{Op: "set_time", U: micros})
	d.mu.Lock()
	d.now = micros
	d.mu.Unlock()
}

func (d *refDriver) Advance(micros uint64) {
	d.call(driverCall{Op: "advance", U: micros})
	d.mu.Lock()
	d.now += micros
	d.mu.Unlock()
}

func (d *refDriver) Now() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now
}

// Close releases the driver. Now keeps reporting the time it was closed at.
func (d *refDriver) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.proc == nil {
		return
	}
	if _, err := d.send(driverCall{Op: "close"}); err != nil {
		d.t.Errorf("libutp-driver: close: %v", err)
	}
	_ = d.proc.Wait()
	d.proc = nil
}

func TestMain(m *testing.M) {
	code := m.Run()
	if recordingDrivers() {
		if err := writeTranscripts(); err != nil {
			fmt.Fprintln(os.Stderr, "writing libutp transcripts:", err)
			code = 1
		}
	}
	os.Exit(code)
}
