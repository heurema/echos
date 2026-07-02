package relayserver

import (
	"fmt"
	"testing"
	"time"
)

// TestRateLimiterEvictsIdleEntries: a source IP with no request for longer
// than ipLimiterIdleTTL is swept from the bookkeeping map, so idle turnover
// cannot grow it without bound.
func TestRateLimiterEvictsIdleEntries(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
	l := NewIPRateLimiter(10)
	l.now = clock.Now

	idle := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	for _, ip := range idle {
		l.Allow(ip)
	}
	if len(l.limiters) != len(idle) {
		t.Fatalf("entries after initial requests = %d, want %d", len(l.limiters), len(idle))
	}

	clock.Advance(ipLimiterIdleTTL + time.Second)
	// Any Allow call sweeps idle entries before touching its own.
	l.Allow("4.4.4.4")

	if len(l.limiters) != 1 {
		t.Fatalf("entries after idle sweep = %d, want 1 (only the fresh entry): %v", len(l.limiters), l.limiters)
	}
	if _, ok := l.limiters["4.4.4.4"]; !ok {
		t.Fatalf("fresh entry should remain after sweep")
	}
	for _, ip := range idle {
		if _, ok := l.limiters[ip]; ok {
			t.Fatalf("idle entry %s should have been evicted", ip)
		}
	}
}

// TestRateLimiterEnforcesCapacityBound: even when more distinct,
// concurrently-active (never-idle) source IPs arrive than
// ipLimiterMaxEntries, the map never exceeds that cap, and it evicts the
// true least-recently-used entry rather than merely the oldest-inserted one.
func TestRateLimiterEnforcesCapacityBound(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
	l := NewIPRateLimiter(10)
	l.now = clock.Now

	ipAt := func(i int) string { return fmt.Sprintf("10.0.%d.%d", i/256, i%256) }

	for i := 0; i < ipLimiterMaxEntries; i++ {
		l.Allow(ipAt(i))
		if len(l.limiters) > ipLimiterMaxEntries {
			t.Fatalf("entry count exceeded cap mid-fill at i=%d: %d", i, len(l.limiters))
		}
	}
	if len(l.limiters) != ipLimiterMaxEntries {
		t.Fatalf("entries after filling to cap = %d, want %d", len(l.limiters), ipLimiterMaxEntries)
	}

	// ipAt(0) is currently the least-recently-used entry (inserted first,
	// never touched since). Touch it so it becomes most-recently-used
	// immediately before the (cap+1)th distinct IP arrives: a true LRU
	// evicts ipAt(1) (now the actual least-recently-used) and keeps
	// ipAt(0), whereas a naive FIFO-by-insertion-order would wrongly evict
	// ipAt(0).
	touched := ipAt(0)
	leastRecentlyUsed := ipAt(1)
	l.Allow(touched)

	newIP := "10.99.99.99"
	l.Allow(newIP)

	if len(l.limiters) != ipLimiterMaxEntries {
		t.Fatalf("entry count after over-cap insert = %d, want %d (bounded)", len(l.limiters), ipLimiterMaxEntries)
	}
	if _, ok := l.limiters[touched]; !ok {
		t.Fatalf("touched entry %s should have survived eviction (LRU, not FIFO)", touched)
	}
	if _, ok := l.limiters[leastRecentlyUsed]; ok {
		t.Fatalf("untouched least-recently-used entry %s should have been evicted", leastRecentlyUsed)
	}
	if _, ok := l.limiters[newIP]; !ok {
		t.Fatalf("newly inserted entry %s should be present", newIP)
	}
}
