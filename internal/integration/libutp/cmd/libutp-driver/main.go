//go:build cgo

// Command libutp-driver serves one libutp.Driver over standard input and
// output, so that tests outside this module can run against real libutp
// without importing it. The root package's conformance tests start it when
// UTP_LIBUTP_DRIVER names its binary.
//
// Each request is one line of JSON, {"op": ..., "u": ..., "b": ...}, and is
// answered by one line, {"i": ..., "b": [...], "s": [...], "err": ...}. The
// first request must be "new". The process exits after "close" or at the end
// of its input.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/arxodeus/utp/internal/integration/libutp"
)

type request struct {
	Op string `json:"op"`
	U  uint64 `json:"u,omitempty"`
	B  []byte `json:"b,omitempty"`
}

type response struct {
	I   int      `json:"i,omitempty"`
	B   [][]byte `json:"b,omitempty"`
	S   []string `json:"s,omitempty"`
	Err string   `json:"err,omitempty"`
}

var socketAddr = regexp.MustCompile(`^0x[0-9a-f]+ `)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "libutp-driver:", err)
		os.Exit(1)
	}
}

func serve() error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 64<<20)
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	var d *libutp.Driver
	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			return err
		}
		if d == nil && req.Op != "new" {
			return fmt.Errorf("%q before new", req.Op)
		}
		var resp response
		switch req.Op {
		case "new":
			var err error
			if d, err = libutp.NewDriver(req.U); err != nil {
				return err
			}
		case "close":
			d.Close()
			if err := enc.Encode(resp); err != nil {
				return err
			}
			return out.Flush()
		case "push_random":
			d.PushRandom(uint32(req.U))
		case "set_udp_mtu":
			d.SetUDPMTU(uint16(req.U))
		case "enable_cc_log":
			d.EnableCCLog()
		case "cc_log":
			// Each line starts with the socket's address, which differs from
			// run to run; drop it so that a recording is reproducible.
			for _, l := range d.CCLog() {
				resp.S = append(resp.S, socketAddr.ReplaceAllString(l, ""))
			}
		case "set_time":
			d.SetTime(req.U)
		case "advance":
			d.Advance(req.U)
		case "connect":
			if err := d.Connect(); err != nil {
				resp.Err = err.Error()
			}
		case "listen":
			d.Listen()
		case "inject":
			resp.I = d.Inject(req.B)
		case "check_timeouts":
			d.CheckTimeouts()
		case "issue_acks":
			d.IssueAcks()
		case "emitted":
			resp.B = d.Emitted()
		case "clear_emitted":
			d.ClearEmitted()
		case "write":
			n, err := d.Write(req.B)
			resp.I = n
			if err != nil {
				resp.Err = err.Error()
			}
		case "close_stream":
			d.CloseStream()
		case "state":
			resp.I = int(d.State())
		default:
			return fmt.Errorf("unknown op %q", req.Op)
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
	return in.Err()
}
