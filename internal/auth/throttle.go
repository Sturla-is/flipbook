package auth

import (
	"sync"
	"time"
)

type throttleBucket struct {
	window time.Time
	count  int
}

type loginThrottle struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	items  map[string]throttleBucket
}

func newLoginThrottle(limit int, window time.Duration, now func() time.Time) *loginThrottle {
	return &loginThrottle{limit: limit, window: window, now: now, items: make(map[string]throttleBucket)}
}

func (t *loginThrottle) allow(keys ...string) (bool, time.Duration) {
	if t == nil || t.limit <= 0 {
		return false, t.window
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.items) > 10000 {
		for key, bucket := range t.items {
			if !now.Before(bucket.window.Add(t.window)) {
				delete(t.items, key)
			}
		}
	}
	blocked := false
	retry := time.Duration(0)
	for _, key := range keys {
		if key == "" {
			continue
		}
		bucket := t.items[key]
		if !now.Before(bucket.window.Add(t.window)) {
			bucket = throttleBucket{window: now}
		}
		if bucket.count >= t.limit {
			blocked = true
			remaining := bucket.window.Add(t.window).Sub(now)
			if remaining > retry {
				retry = remaining
			}
		}
	}
	if blocked {
		return false, retry
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		bucket := t.items[key]
		if !now.Before(bucket.window.Add(t.window)) {
			bucket = throttleBucket{window: now}
		}
		bucket.count++
		t.items[key] = bucket
	}
	return true, 0
}
