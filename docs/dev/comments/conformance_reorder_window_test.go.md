# conformance_reorder_window_test.go

Every comment in `conformance_reorder_window_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 7 — func TestReorderedDataCostsUsNoMoreWindowThanLibutp

```
// Out-of-order data costs us no more receive window than it costs libutp.
//
// It used to cost all of it. `receiveBuffer.Available()` subtracted every byte
// held behind a gap from the window this library advertised, where libutp's
// `get_rcv_window` (`utp_internal.cpp:590-596`) counts only what its embedder
// has not yet read -- and a packet held out of order sits in `conn->inbuf`,
// never reaching `utp_call_on_read` until the gap before it is filled, so it
// never enters that figure.
//
// Measured before the split into Window and Available:
//
//	40,000 bytes held       we advertised 1,008,576   libutp 1,048,576
//	1,120,000 bytes held    we advertised     1,376   libutp 1,048,576
//
// The second row is the one that mattered. 1,376 bytes is under the 1,400 a
// packet needs, so the peer could send nothing -- including the retransmission
// that would have filled the gap and let the whole buffer be delivered. And it
// is above zero, so the peer's zero-window probe, which tests for exactly
// zero, never armed. Both ends went silent. That was the cause of a stall a
// network case reached in 4 runs out of 20.
//
// These cases now pin the agreement rather than the divergence. If they start
// failing because our window falls again, the stall is back.
```

## Line 41 — func TestReorderedDataCostsUsNoMoreWindowThanLibutp

```
// +2 onwards: +1 is the gap, and it never arrives.
```

## Line 54 — func TestReorderedDataCostsUsNoMoreWindowThanLibutp

```
// The control: libutp has to be holding the data and still advertising
	// room, or there is no agreement worth checking.
```

## Line 70 — func TestReorderedDataDoesNotBlockOurWindow

```
// The same, driven to where it used to block the peer entirely.
//
// 800 packets behind a gap is more than a megabyte -- enough to have taken the
// old accounting under the size of a single packet. The reorder window bounds
// it there: outsideReorderWindow drops anything more than 1024 packets past
// the gap, as libutp does.
```

## Line 113 — func windowsAfter

```
// windowsAfter drives both implementations through the same packets and
// returns the receive window each ends up advertising.
```
