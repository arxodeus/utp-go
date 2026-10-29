# netem/latecomer_long_test.go

Every comment in `netem/latecomer_long_test.go` at e56c9aa, verbatim.

## Line 15 — func TestLatecomerShareOverTime

```
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
```

## Line 45 — func TestLatecomerShareOverTime

```
// The same link as runLatecomer.
```
