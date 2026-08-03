// Package ratelimit provides a small in-memory fixed-window limiter.
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	started time.Time
	count   int
}

// Limiter bounds repeated attempts for one process. It deliberately has a
// small interface so HTTP handlers need not know its storage details.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	limit   int
	window  time.Duration
	entries map[string]entry
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{now: time.Now, limit: limit, window: window, entries: make(map[string]entry)}
}

// Allow records one attempt for key. It returns the remaining retry duration
// when the key has exhausted its window.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.entries[key]
	if !ok || !now.Before(current.started.Add(l.window)) {
		l.entries[key] = entry{started: now, count: 1}
		return true, 0
	}
	if current.count >= l.limit {
		return false, current.started.Add(l.window).Sub(now)
	}
	current.count++
	l.entries[key] = current
	return true, 0
}
