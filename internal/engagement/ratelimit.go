package engagement

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type rateEntry struct {
	windowStart time.Time
	count       int
}

type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	limit   int
	window  time.Duration
	now     func() time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 10
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &RateLimiter{
		entries: make(map[string]rateEntry), limit: limit, window: window,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (l *RateLimiter) Allow(parts ...string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	digest := sha256.New()
	for _, part := range parts {
		digest.Write([]byte{0})
		digest.Write([]byte(part))
	}
	key := hex.EncodeToString(digest.Sum(nil))
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry = rateEntry{windowStart: now}
	}
	if entry.count >= l.limit {
		retryAfter := l.window - now.Sub(entry.windowStart)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}
	entry.count++
	l.entries[key] = entry
	if len(l.entries) > 10000 {
		for candidate, value := range l.entries {
			if now.Sub(value.windowStart) >= l.window {
				delete(l.entries, candidate)
			}
		}
	}
	return true, 0
}
