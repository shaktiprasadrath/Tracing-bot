package sampler

import (
	"testing"
	"time"
)

// TestTokenBucket_BurstThenRefill covers the token bucket backing
// rare_path_keeps_per_min / floor_traces_per_min_per_service (F02 §3.1
// FR-F02-3): capacity tokens are available immediately (burst), exhausted
// after that, and refill at perMinute/60 tokens per second thereafter.
func TestTokenBucket_BurstThenRefill(t *testing.T) {
	b := newTokenBucket(60, 0) // 60/min == 1/sec

	allowed := 0
	for i := 0; i < 100; i++ {
		if b.Take(0) {
			allowed++
		}
	}
	if allowed != 60 {
		t.Fatalf("initial burst allowed %d takes at t=0, want exactly the capacity (60)", allowed)
	}
	if b.Take(0) {
		t.Fatalf("token available immediately after the initial burst exhausted capacity")
	}

	// After exactly 1 simulated second, exactly one token should have
	// refilled (60/min == 1/sec).
	oneSecond := int64(time.Second)
	if !b.Take(oneSecond) {
		t.Fatalf("expected exactly one token to be available after 1s at a 60/min refill rate")
	}
	if b.Take(oneSecond) {
		t.Fatalf("a second token was available after only 1s elapsed; refill rate is not being respected")
	}

	// After 30 more seconds, 30 more tokens should be available (capped at
	// capacity, never over-filling).
	thirtySecondsLater := oneSecond + 30*int64(time.Second)
	got := 0
	for i := 0; i < 100; i++ {
		if b.Take(thirtySecondsLater) {
			got++
		}
	}
	if got != 30 {
		t.Fatalf("after 30s refill allowed %d takes, want exactly 30", got)
	}
}

// TestTokenBucket_NeverExceedsCapacity covers the "capped at capacity" half
// of the refill rule: a very long idle gap must not let the bucket accrue
// more than `capacity` tokens.
func TestTokenBucket_NeverExceedsCapacity(t *testing.T) {
	b := newTokenBucket(10, 0)
	farFuture := int64(24 * time.Hour)
	allowed := 0
	for i := 0; i < 1000; i++ {
		if b.Take(farFuture) {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("bucket allowed %d takes after a long idle gap, want capped at capacity (10)", allowed)
	}
}
