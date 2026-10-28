# base_delay_hist_test.go

Every comment in `base_delay_hist_test.go` at e56c9aa, verbatim.

## Line 8 — func TestBaseDelayRemembersThirteenMinutes

```
// The base delay is libutp's DelayHist (utp_internal.cpp:260-381): thirteen
// one-minute buckets by default, so a low sample is remembered for twelve to
// thirteen minutes of samples, and then forgotten.
```

## Line 18 — func TestBaseDelayRemembersThirteenMinutes

```
// the empty path, once
```

## Line 35 — func TestBaseDelayDoesNotAgeWhileIdle

```
// Only a sample rotates the history, as in libutp: a connection that falls
// silent for twenty minutes still has its base when it speaks again, the
// first sample after the silence moving it on by one bucket only.
```

## Line 49 — func TestBaseDelayShiftAgesOut

```
// A drift correction raises the base at once and lasts only as long as the
// buckets carrying it: libutp's shift adds to every bucket, and each is
// replaced by a fresh sample as the history rotates onto it.
```
