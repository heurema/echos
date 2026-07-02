package relayserver

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// IPRateLimiter enforces a per-source-IP requests-per-minute budget.
type IPRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	limit    rate.Limit
	burst    int
}

// NewIPRateLimiter allows up to requestsPerMinute requests per minute,
// per source IP, with a burst equal to that same count.
func NewIPRateLimiter(requestsPerMinute int) *IPRateLimiter {
	return &IPRateLimiter{
		limiters: map[string]*rate.Limiter{},
		limit:    rate.Limit(float64(requestsPerMinute) / 60.0),
		burst:    requestsPerMinute,
	}
}

// Allow reports whether a request from ip may proceed right now.
func (l *IPRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.limit, l.burst)
		l.limiters[ip] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// clientIP extracts the source IP from a request's RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
