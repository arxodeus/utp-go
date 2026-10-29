# window_credit_test.go

Every comment in `window_credit_test.go` at e56c9aa, verbatim.

## Line 10 — func TestWindowGrowthDoesNotDependOnHowAcksAreSplit

```
// A round trip's acknowledgements grow the window by the same amount however
// many acknowledgements it is split into: a full window of them adds the
// gain once, scaled by how far off target the delay is.
//
// libutp's window factor divides each acknowledgement's bytes by the window
// as it stands, which the previous acknowledgement of the same round trip has
// just grown, so the more acknowledgements, the less growth: about G^2/2W
// short of G for an update per packet (utp_internal.cpp:1668). Here each is
// reckoned against the window its packets were sent in. DEVIATIONS.md, "The
// window grows by a round trip's worth per round trip".
```

## Line 61 — func TestWindowGrowthDoesNotDependOnHowAcksAreSplit

```
// Each update truncates the window to whole bytes, as libutp's
			// does, so up to a byte apiece.
```

## Line 70 — func TestWindowGrowthDoesNotDependOnHowAcksAreSplit

```
// libutp's factor, for the record: the split costs growth.
```
