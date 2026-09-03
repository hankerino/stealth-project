// Per-account rate limiting for the trading-engine API gateway (M1).
//
// A token-bucket limiter implemented with the standard library only (no
// golang.org/x/time/rate): the gateway stays 100% stdlib so every file compiles
// and unit-tests without fetching modules, and CI `go mod tidy` pulls nothing
// new. Each account gets its own bucket; idle buckets are swept periodically to
// bound memory.
package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// tokenBucket is a classic token bucket: up to `burst` tokens, refilled at
// `rps` tokens per second.
type tokenBucket struct {
	tokens   float64
	lastFill time.Time
	lastSeen time.Time
}

func (b *tokenBucket) allow(now time.Time, rps, burst float64) bool {
	// Refill based on elapsed time.
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rps
		if b.tokens > burst {
			b.tokens = burst
		}
		b.lastFill = now
	}
	b.lastSeen = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// rateLimiter holds one bucket per account key.
type rateLimiter struct {
	rps   float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

func newRateLimiter(rps, burst float64) *rateLimiter {
	rl := &rateLimiter{
		rps:     rps,
		burst:   burst,
		now:     time.Now,
		buckets: make(map[string]*tokenBucket),
	}
	go rl.sweepLoop()
	return rl
}

// allow reports whether a request for `key` may proceed now.
func (rl *rateLimiter) allow(key string) bool {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, lastFill: now, lastSeen: now}
		rl.buckets[key] = b
	}
	return b.allow(now, rl.rps, rl.burst)
}

// sweepLoop evicts buckets unused for a while, bounding memory.
func (rl *rateLimiter) sweepLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := rl.now().Add(-30 * time.Minute)
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// middleware rate-limits per authenticated account. It MUST run after auth so
// claims are present; unauthenticated requests are limited by a shared "anon"
// key as a coarse backstop.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := "anon"
		if c, ok := claimsFrom(r.Context()); ok && c.Subject != "" {
			key = c.Subject
		}
		if !rl.allow(key) {
			w.Header().Set("Retry-After", strconv.Itoa(1))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
