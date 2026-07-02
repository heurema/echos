package relayserver

import (
	"container/list"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// ipLimiterIdleTTL bounds memory under normal turnover: an entry not
	// touched by a request from its source IP for longer than this is
	// evicted on the next Allow call.
	ipLimiterIdleTTL = 30 * time.Minute
	// ipLimiterMaxEntries hard-caps the map even when more distinct source
	// IPs than this are simultaneously active (so none goes idle): once
	// full, the least-recently-used entry is evicted to make room.
	ipLimiterMaxEntries = 10000
)

type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	elem     *list.Element // this entry's node in lru, for O(1) touch/evict
}

// IPRateLimiter enforces a per-source-IP requests-per-minute budget. Its
// bookkeeping map is bounded two ways: entries idle longer than
// ipLimiterIdleTTL are swept away, and a hard cap of ipLimiterMaxEntries is
// enforced by evicting the least-recently-used entry.
type IPRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiterEntry
	lru      *list.List // front = most recently used, back = least
	limit    rate.Limit
	burst    int
	now      func() time.Time
}

// NewIPRateLimiter allows up to requestsPerMinute requests per minute,
// per source IP, with a burst equal to that same count.
func NewIPRateLimiter(requestsPerMinute int) *IPRateLimiter {
	return &IPRateLimiter{
		limiters: map[string]*ipLimiterEntry{},
		lru:      list.New(),
		limit:    rate.Limit(float64(requestsPerMinute) / 60.0),
		burst:    requestsPerMinute,
		now:      time.Now,
	}
}

// Allow reports whether a request from ip may proceed right now.
func (l *IPRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	now := l.now()
	l.evictIdleLocked(now)

	e, ok := l.limiters[ip]
	if ok {
		l.lru.MoveToFront(e.elem)
		e.lastSeen = now
	} else {
		if len(l.limiters) >= ipLimiterMaxEntries {
			l.evictLRULocked()
		}
		e = &ipLimiterEntry{limiter: rate.NewLimiter(l.limit, l.burst), lastSeen: now}
		e.elem = l.lru.PushFront(ip)
		l.limiters[ip] = e
	}
	l.mu.Unlock()
	return e.limiter.Allow()
}

// evictIdleLocked drops every entry whose source IP has not been seen in
// over ipLimiterIdleTTL. Callers must hold l.mu.
func (l *IPRateLimiter) evictIdleLocked(now time.Time) {
	for back := l.lru.Back(); back != nil; back = l.lru.Back() {
		ip := back.Value.(string)
		e := l.limiters[ip]
		if now.Sub(e.lastSeen) <= ipLimiterIdleTTL {
			break // lru is ordered most- to least-recently-used; the rest are fresher
		}
		l.lru.Remove(back)
		delete(l.limiters, ip)
	}
}

// evictLRULocked drops the single least-recently-used entry. Callers must
// hold l.mu.
func (l *IPRateLimiter) evictLRULocked() {
	back := l.lru.Back()
	if back == nil {
		return
	}
	ip := back.Value.(string)
	l.lru.Remove(back)
	delete(l.limiters, ip)
}

// clientIP extracts the source IP from a request's RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
