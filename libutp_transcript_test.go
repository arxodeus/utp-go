package utp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Recorded libutp transcripts: one gzipped JSON file per top-level test in
// testdata/libutp, holding every driver that test created, in order.
//
// A transcript is kept small three ways. An argument longer than
// maxInlineArg is stored as its SHA-256, which is enough to check that the
// same call was made. A packet libutp returned is stored once per file and
// referred to by index. And a run of identical calls, such as a test
// stepping the clock until something happens, is stored once with a repeat
// count.

const (
	transcriptDir = "testdata/libutp"
	maxInlineArg  = 64
)

// driverCall is one call and what libutp returned.
type driverCall struct {
	Op  string
	U   uint64
	B   []byte
	RI  int
	RB  [][]byte
	RS  []string
	Err string
}

// storedCall is a driverCall as written to a transcript.
type storedCall struct {
	Op  string   `json:"o"`
	U   uint64   `json:"u,omitempty"`
	Arg string   `json:"a,omitempty"`
	RI  int      `json:"ri,omitempty"`
	RP  []int    `json:"rp,omitempty"`
	RS  []string `json:"rs,omitempty"`
	Err string   `json:"e,omitempty"`
}

// storedEntry is one call, or a run of calls repeated Repeat times.
type storedEntry struct {
	Repeat int          `json:"n,omitempty"`
	Calls  []storedCall `json:"c"`
}

type storedDriver struct {
	Start   uint64        `json:"start"`
	Entries []storedEntry `json:"calls"`
}

type storedFile struct {
	Packets [][]byte                  `json:"packets"`
	Tests   map[string][]storedDriver `json:"tests"`
}

// encodeArg is how an argument is stored and compared.
func encodeArg(b []byte) string {
	if b == nil {
		return ""
	}
	if len(b) <= maxInlineArg {
		return base64.StdEncoding.EncodeToString(b) + "="
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// recordedDriver collects one live driver's calls.
type recordedDriver struct {
	start uint64
	calls []driverCall
}

var transcripts = struct {
	sync.Mutex
	loaded   map[string]*storedFile                  // replay: file -> contents
	recorded map[string]map[string][]*recordedDriver // record: file -> test name -> drivers
	created  map[any]int                             // drivers created per test instance
}{
	loaded:   map[string]*storedFile{},
	recorded: map[string]map[string][]*recordedDriver{},
	created:  map[any]int{},
}

// transcriptFile is the file holding the transcripts of the top-level test
// of name.
func transcriptFile(name string) string {
	top, _, _ := strings.Cut(name, "/")
	return filepath.Join(transcriptDir, top+".json.gz")
}

// loadTranscripts reads file once. Called with the lock held.
func loadTranscripts(file string) (*storedFile, error) {
	if f, ok := transcripts.loaded[file]; ok {
		return f, nil
	}
	f := &storedFile{Tests: map[string][]storedDriver{}}
	raw, err := os.Open(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		defer raw.Close()
		zr, err := gzip.NewReader(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		data, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if err := json.Unmarshal(data, f); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	transcripts.loaded[file] = f
	return f, nil
}

// replayCursor walks a stored driver's calls, expanding repeats as it goes.
type replayCursor struct {
	file    *storedFile
	entries []storedEntry
	entry   int // current entry
	round   int // repetition of the current entry
	call    int // call within the current repetition
	index   int // calls taken so far
}

// next returns the next recorded call, or false at the end.
func (c *replayCursor) next() (storedCall, bool) {
	for c.entry < len(c.entries) {
		e := c.entries[c.entry]
		repeat := max(e.Repeat, 1)
		if c.round < repeat && c.call < len(e.Calls) {
			sc := e.Calls[c.call]
			c.call++
			if c.call == len(e.Calls) {
				c.call = 0
				c.round++
			}
			c.index++
			return sc, true
		}
		c.entry++
		c.round, c.call = 0, 0
	}
	return storedCall{}, false
}

// done reports whether every recorded call has been taken.
func (c *replayCursor) done() bool {
	save := *c
	_, more := c.next()
	*c = save
	return !more
}

// result fills in what libutp returned for sc.
func (c *replayCursor) result(sc storedCall, call driverCall) (driverCall, error) {
	call.RI, call.RS, call.Err = sc.RI, sc.RS, sc.Err
	for _, i := range sc.RP {
		if i < 0 || i >= len(c.file.Packets) {
			return call, fmt.Errorf("packet %d out of range", i)
		}
		call.RB = append(call.RB, c.file.Packets[i])
	}
	return call, nil
}

// writeTranscripts saves what was recorded, one file per top-level test,
// replacing what the file held.
func writeTranscripts() error {
	transcripts.Lock()
	defer transcripts.Unlock()
	if len(transcripts.recorded) == 0 {
		return nil
	}
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		return err
	}
	for file, tests := range transcripts.recorded {
		out := storedFile{Packets: [][]byte{}, Tests: map[string][]storedDriver{}}
		index := map[string]int{}
		intern := func(p []byte) int {
			if i, ok := index[string(p)]; ok {
				return i
			}
			index[string(p)] = len(out.Packets)
			out.Packets = append(out.Packets, p)
			return len(out.Packets) - 1
		}
		names := make([]string, 0, len(tests))
		for name := range tests {
			names = append(names, name)
		}
		// Packets are numbered in test order, so a re-recording that
		// changes nothing writes the same file.
		slices.Sort(names)
		for _, name := range names {
			for _, d := range tests[name] {
				calls := make([]storedCall, len(d.calls))
				for i, c := range d.calls {
					sc := storedCall{Op: c.Op, U: c.U, Arg: encodeArg(c.B), RI: c.RI, RS: c.RS, Err: c.Err}
					for _, p := range c.RB {
						sc.RP = append(sc.RP, intern(p))
					}
					calls[i] = sc
				}
				out.Tests[name] = append(out.Tests[name], storedDriver{Start: d.start, Entries: foldCalls(calls)})
			}
		}
		data, err := json.Marshal(out)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := zw.Write(data); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// foldCalls stores each run of a block of up to eight calls, repeated at
// least twice, once with its count.
func foldCalls(calls []storedCall) []storedEntry {
	var out []storedEntry
	for i := 0; i < len(calls); {
		bestRounds, bestLen := 0, 0
		for k := 1; k <= 8 && i+2*k <= len(calls); k++ {
			rounds := 1
			for i+(rounds+1)*k <= len(calls) && reflect.DeepEqual(calls[i+rounds*k:i+(rounds+1)*k], calls[i:i+k]) {
				rounds++
			}
			if rounds >= 2 && rounds*k > bestRounds*bestLen {
				bestRounds, bestLen = rounds, k
			}
		}
		if bestRounds > 0 {
			out = append(out, storedEntry{Repeat: bestRounds, Calls: calls[i : i+bestLen]})
			i += bestRounds * bestLen
			continue
		}
		out = append(out, storedEntry{Calls: calls[i : i+1]})
		i++
	}
	return out
}
