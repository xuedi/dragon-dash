package auth

import (
	"sync"
	"time"
)

// Limiter locks out an address after too many failed logins. It counts per
// address only: a per-account lock would let anyone lock the owner out on
// purpose by failing with the owner's name.
type Limiter struct {
	mu           sync.Mutex
	max          int
	window, lock time.Duration
	m            map[string]*attempts
	now          func() time.Time
}

// Stale entries are swept only once the map is this big, which keeps a quiet
// LAN from paying for a sweep on every failed login.
const pruneAt = 1024

type attempts struct {
	first  time.Time
	count  int
	locked time.Time
}

// NewLimiter locks an address for lock after max failures within window.
func NewLimiter(max int, window, lock time.Duration) *Limiter {
	return &Limiter{max: max, window: window, lock: lock, m: map[string]*attempts{}, now: time.Now}
}

// Allowed reports whether addr may try again, and if not, for how long it has
// to wait.
func (l *Limiter) Allowed(addr string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[addr]
	if a == nil {
		return true, 0
	}
	if wait := a.locked.Sub(l.now()); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Fail records a failed attempt and reports whether it locked the address.
func (l *Limiter) Fail(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.m) >= pruneAt {
		for k, a := range l.m {
			if now.Sub(a.first) > l.window && !now.Before(a.locked) {
				delete(l.m, k)
			}
		}
	}
	a := l.m[addr]
	if a == nil || now.Sub(a.first) > l.window {
		a = &attempts{first: now}
		l.m[addr] = a
	}
	a.count++
	if a.count >= l.max {
		a.locked = now.Add(l.lock)
		a.count = 0
		a.first = now
		return true
	}
	return false
}

// Reset forgets an address after a successful login.
func (l *Limiter) Reset(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, addr)
}
