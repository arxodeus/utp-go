# keepalive_release_test.go

Every comment in `keepalive_release_test.go` at e56c9aa, verbatim.

## Line 10 — func TestQuietConnectionReleasesReceiveStorage

```
// A connection quiet for its keep-alive interval holds none of its receive
// buffer's storage, as libutp holds none of what it has delivered; one that
// is still busy keeps it.
```
