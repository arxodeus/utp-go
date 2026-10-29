# path_mtu_cache_test.go

Every comment in `path_mtu_cache_test.go` at e56c9aa, verbatim.

## Line 9 — func TestPathMTUCacheIsBounded

```
// The path MTU cache is bounded, however many destinations the process
// sends to. It removed nothing, so a node reaching ever more addresses grew
// it for as long as it ran. From the release review of 9c44c4f (R10).
```

## Line 35 — func TestPathMTUCachePrunesExpiredFirst

```
// Expired entries are what goes first.
```

## Line 57 — func TestPathMTUCacheKeysByZone

```
// Two link-local peers with the same address on different interfaces have
// a cache entry each.
```
