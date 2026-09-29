package netem

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// TestLatecomerShareOverTime is the latecomer experiment over 16 MB a flow,
// reporting the latecomer's share of each two seconds while both run.
//
// TestLatecomerShare's 4 MB transfers end within a slowdown period or two, so
// they cannot tell a start-up transient from a persistent advantage; this
// can. It found LEDBAT++'s latecomer advantage decaying from about 85% to
// about 62% over twenty seconds rather than correcting (KNOWN-LIMITATIONS.md,
// "LEDBAT++'s latecomer takes most of the link").
//
// Opt-in, because it takes about half a minute: set UTP_LONG_LATECOMER=1.
// UTP_LONG_LATECOMER_ALGO=ledbat runs classic LEDBAT instead of LEDBAT++, and
// UTP_LONG_LATECOMER_MB sets the size of each flow's transfer (default 16),
// and UTP_LONG_LATECOMER_ARRIVAL how long after the incumbent the latecomer
// starts, as a Go duration (default 1.5s, TestLatecomerShare's). The split
// depends on it: a latecomer arriving before the incumbent has settled is
// starved rather than favoured (KNOWN-LIMITATIONS.md, "LEDBAT++'s latecomer
// takes most of the link").
func TestLatecomerShareOverTime(t *testing.T) {
	if os.Getenv("UTP_LONG_LATECOMER") == "" {
		t.Skip("set UTP_LONG_LATECOMER=1 to run")
	}
	algo := utp.AlgorithmLEDBATPP
	if os.Getenv("UTP_LONG_LATECOMER_ALGO") == "ledbat" {
		algo = utp.AlgorithmLEDBAT
	}

	n := NewNetwork(210)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	// The same link as runLatecomer.
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024}
	n.ConnectAsymmetric(sender, receiver, cfg, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, sender, receiver, quiet())
	megabytes := 16
	if v := os.Getenv("UTP_LONG_LATECOMER_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			megabytes = n
		}
	}
	data := make([]byte, megabytes<<20)

	type outcome struct {
		delivered []DeliverySample
		err       error
	}
	incumbentCh := make(chan outcome, 1)
	latecomerCh := make(chan outcome, 1)
	run := func(cid uint16, out chan<- outcome) {
		c := utp.NewConnectionConfig()
		c.CongestionAlgorithm = algo
		r, err := pair.RunTransfer(ctx, data, FlowOptions{
			InitiatorCid:    cid,
			Config:          c,
			MetricsInterval: 10 * time.Millisecond,
		})
		if err != nil {
			out <- outcome{nil, err}
			return
		}
		out <- outcome{ReceivedSeries(r.Receiver.Samples()), nil}
	}
	go run(700, incumbentCh)
	arrival := 1500 * time.Millisecond
	if v := os.Getenv("UTP_LONG_LATECOMER_ARRIVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("UTP_LONG_LATECOMER_ARRIVAL: %v", err)
		}
		arrival = d
	}
	time.Sleep(arrival)
	go run(720, latecomerCh)
	inc, late := <-incumbentCh, <-latecomerCh
	if inc.err != nil {
		t.Fatalf("incumbent: %v", inc.err)
	}
	if late.err != nil {
		t.Fatalf("latecomer: %v", late.err)
	}

	var per []string
	for from := late.delivered[0].At; ; from = from.Add(2 * time.Second) {
		until := from.Add(2 * time.Second)
		if until.After(inc.delivered[len(inc.delivered)-1].At) ||
			until.After(late.delivered[len(late.delivered)-1].At) {
			break
		}
		dl := float64(bytesAt(late.delivered, until) - bytesAt(late.delivered, from))
		di := float64(bytesAt(inc.delivered, until) - bytesAt(inc.delivered, from))
		per = append(per, fmt.Sprintf("%.0f", 100*dl/(dl+di)))
	}
	share, ok := SplitWhileBothRan(late.delivered, inc.delivered)
	if !ok {
		t.Fatal("the two flows never overlapped")
	}
	t.Logf("%s: the latecomer's share while both ran %.1f%%; per two seconds: %s",
		algo, 100*share, strings.Join(per, " "))
}
