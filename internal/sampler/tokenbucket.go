package sampler

import "sync"

// tokenBucket is a minimal, clock-driven (DR-31: no time.Now/time.After)
// token bucket used for the rare-path (rare_path_keeps_per_min) and
// per-service floor (floor_traces_per_min_per_service) token bucket rules
// in F02 §3.1 FR-F02-3.
type tokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	refillRate float64 // tokens per second
	tokens     float64
	lastRefill int64 // unix nanos
}

func newTokenBucket(perMinute float64, nowUnixNano int64) *tokenBucket {
	return &tokenBucket{
		capacity:   perMinute,
		refillRate: perMinute / 60.0,
		tokens:     perMinute,
		lastRefill: nowUnixNano,
	}
}

// Take reports whether a token was available at nowUnixNano, consuming one
// if so.
func (b *tokenBucket) Take(nowUnixNano int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if nowUnixNano > b.lastRefill {
		elapsedSec := float64(nowUnixNano-b.lastRefill) / 1e9
		b.tokens += elapsedSec * b.refillRate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.lastRefill = nowUnixNano
	}
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}
