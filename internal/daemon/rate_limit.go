package daemon

import (
	"net/http"
	"sync"
	"time"
)

const (
	defaultRateLimitTTL = 10 * time.Minute
)

// IPRateLimiter implements a simple per-IP token bucket rate limiter.
// It is safe for concurrent use by multiple goroutines.
type IPRateLimiter struct {
	mu          sync.Mutex
	qps         float64
	burst       float64
	ttl         time.Duration
	now         func() time.Time
	lastCleanup time.Time
	entries     map[string]*ipRateEntry
}

type ipRateEntry struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// NewIPRateLimiter creates a per-IP limiter. If qps or burst are non-positive,
// it returns nil to indicate rate limiting is disabled.
func NewIPRateLimiter(qps float64, burst int) *IPRateLimiter {
	if qps <= 0 || burst <= 0 {
		return nil
	}
	return &IPRateLimiter{
		qps:     qps,
		burst:   float64(burst),
		ttl:     defaultRateLimitTTL,
		now:     time.Now,
		entries: make(map[string]*ipRateEntry),
	}
}

func (l *IPRateLimiter) Allow(remoteAddr string) bool {
	if l == nil {
		return true
	}
	ip := parseRemoteIP(remoteAddr)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	ipKey := ip.String()
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupLocked(now)

	entry := l.entries[ipKey]
	if entry == nil {
		entry = &ipRateEntry{
			tokens:   l.burst,
			last:     now,
			lastSeen: now,
		}
		l.entries[ipKey] = entry
	}
	entry.lastSeen = now

	if now.After(entry.last) {
		elapsed := now.Sub(entry.last).Seconds()
		entry.tokens += elapsed * l.qps
		if entry.tokens > l.burst {
			entry.tokens = l.burst
		}
		entry.last = now
	}

	if entry.tokens >= 1 {
		entry.tokens -= 1
		return true
	}
	return false
}

func (l *IPRateLimiter) cleanupLocked(now time.Time) {
	if l.ttl <= 0 {
		return
	}
	if !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < l.ttl {
		return
	}
	for ip, entry := range l.entries {
		if entry == nil {
			delete(l.entries, ip)
			continue
		}
		if now.Sub(entry.lastSeen) > l.ttl {
			delete(l.entries, ip)
		}
	}
	l.lastCleanup = now
}

func writeRateLimitExceeded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
}
