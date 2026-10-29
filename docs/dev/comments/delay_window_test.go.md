# delay_window_test.go

Every comment in `delay_window_test.go` at e56c9aa, verbatim.

## Line 9 — type stepClock

```
// stepClock is a clock that only tells the time, moved by hand.
```

## Line 17 — func TestDelayWindowMatchesAScan

```
// The base delay is the least sample within the window, as a scan over every
// sample would give it, for samples arriving in time order.
```

## Line 51 — func TestDelayWindowStaysSmall

```
// What a busy connection keeps: two minutes of acknowledgements at a
// thousand a second, delays wandering as a queue's do. The heap this replaced
// popped a sample only once its least had expired, so it held more than the
// window: up to 239,689 samples here, 32 bytes each, 7.7 MB. This holds at
// most 495.
```

## Line 65 — func TestDelayWindowStaysSmall

```
// a random walk, in microseconds
```
