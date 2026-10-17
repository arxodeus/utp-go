package utp

import (
	"net"
	"testing"
	"time"
)

// The path MTU cache is bounded, however many destinations the process
// sends to. It removed nothing, so a node reaching ever more addresses grew
// it for as long as it ran. From the release review of 9c44c4f (R10).
func TestPathMTUCacheIsBounded(t *testing.T) {
	pathMTUCache.Lock()
	saved := pathMTUCache.entries
	pathMTUCache.entries = make(map[string]pathMTUEntry)
	pathMTUCache.Unlock()
	defer func() {
		pathMTUCache.Lock()
		pathMTUCache.entries = saved
		pathMTUCache.Unlock()
	}()

	for i := 0; i < 3*pathMTUCacheMax; i++ {
		ip := net.IPv4(10, byte(i>>16), byte(i>>8), byte(i))
		pathMTUFor(&net.UDPAddr{IP: ip, Port: 6881})
	}
	pathMTUCache.Lock()
	n := len(pathMTUCache.entries)
	pathMTUCache.Unlock()
	if n > pathMTUCacheMax {
		t.Fatalf("%d entries after %d destinations, want at most %d", n, 3*pathMTUCacheMax, pathMTUCacheMax)
	}
}

// Expired entries are what goes first.
func TestPathMTUCachePrunesExpiredFirst(t *testing.T) {
	pathMTUCache.Lock()
	saved := pathMTUCache.entries
	now := time.Now()
	pathMTUCache.entries = make(map[string]pathMTUEntry)
	for i := 0; i < pathMTUCacheMax; i++ {
		at := now
		if i%2 == 0 {
			at = now.Add(-2 * pathMTUCacheTTL)
		}
		pathMTUCache.entries[net.IPv4(10, 0, byte(i>>8), byte(i)).String()] = pathMTUEntry{at: at}
	}
	prunePathMTUCache(now)
	n := len(pathMTUCache.entries)
	pathMTUCache.entries = saved
	pathMTUCache.Unlock()
	if n != pathMTUCacheMax/2 {
		t.Fatalf("%d entries left, want the %d fresh ones", n, pathMTUCacheMax/2)
	}
}

// Two link-local peers with the same address on different interfaces have
// a cache entry each.
func TestPathMTUCacheKeysByZone(t *testing.T) {
	pathMTUCache.Lock()
	saved := pathMTUCache.entries
	pathMTUCache.entries = make(map[string]pathMTUEntry)
	pathMTUCache.Unlock()
	defer func() {
		pathMTUCache.Lock()
		pathMTUCache.entries = saved
		pathMTUCache.Unlock()
	}()
	ip := net.ParseIP("fe80::1")
	pathMTUFor(&net.UDPAddr{IP: ip, Port: 1, Zone: "eth0"})
	pathMTUFor(&net.UDPAddr{IP: ip, Port: 1, Zone: "eth1"})
	pathMTUCache.Lock()
	n := len(pathMTUCache.entries)
	pathMTUCache.Unlock()
	if n != 2 {
		t.Fatalf("%d entries for one address on two interfaces, want 2", n)
	}
}
