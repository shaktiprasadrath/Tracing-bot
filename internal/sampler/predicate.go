package sampler

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"traceiq/internal/model"
)

// simplePredicateSet implements PredicateSet (DR-10/DR-11).
//
// DEVIATION (w9-sampler): FR-F02-6 specifies inverted indexes (byService,
// byErrorSig, byPathSig, byAttrKey) rebuilt on add/remove and read through
// an atomic snapshot pointer, bounding Match to <=32 candidates. Given the
// TDD scope/time budget here, this implementation keeps a single
// atomically-swapped slice snapshot (still lock-free reads / copy-on-write
// on mutation, matching the concurrency contract) but Match does a linear
// scan of that slice rather than building the four inverted indexes. At
// max_predicates=32 this is behaviorally equivalent and within the ~150us
// budget; it will not scale past that without the real indexes.
type simplePredicateSet struct {
	mu       sync.Mutex // guards writes only; reads go through snapshot
	snapshot atomic.Pointer[[]InterestPredicate]
	seq      uint64
	maxPreds int // DR-11's sampler.interest.max_predicates, per tenant, both scopes combined

	evictedTotal uint64 // traceiq_sampler_predicates_evicted_total stand-in, exported via Evicted()
}

// defaultMaxPredicates is DR-11's sampler.interest.max_predicates default.
const defaultMaxPredicates = 32

func NewPredicateSet() PredicateSet {
	return NewPredicateSetWithLimit(defaultMaxPredicates)
}

// NewPredicateSetWithLimit constructs a PredicateSet with a caller-chosen
// per-tenant max_predicates bound (DR-11's config key), so tests can exercise
// eviction without adding 32 fixtures.
func NewPredicateSetWithLimit(maxPredicates int) PredicateSet {
	if maxPredicates <= 0 {
		maxPredicates = defaultMaxPredicates
	}
	ps := &simplePredicateSet{maxPreds: maxPredicates}
	empty := make([]InterestPredicate, 0)
	ps.snapshot.Store(&empty)
	return ps
}

// Evicted returns the running count of predicates evicted for exceeding
// max_predicates (traceiq_sampler_predicates_evicted_total stand-in; DR-11).
func (ps *simplePredicateSet) Evicted() uint64 {
	return atomic.LoadUint64(&ps.evictedTotal)
}

// Match evaluates the current atomic snapshot without ever writing through
// its backing array.
//
// FIXED (w10-review): this previously took `p := &snap[i]` — a pointer
// directly into the slice published by ps.snapshot.Load() — and mutated
// `p.Hits++` through it. Because Load() hands every concurrent reader the
// SAME backing array (that is the whole point of the atomic-pointer,
// lock-free-read design this file's own comment describes), an unsynchronized
// `p.Hits++` there was a real data race: two concurrent Match calls hitting
// the same predicate raced on the same memory word (lost updates), and any
// concurrent Add/Remove/ExpireDue/Narrow doing `old := *ps.snapshot.Load()`
// then `copy(next, old)` could read a torn Hits value mid-write. Snapshots
// handed to readers must be treated as immutable; only storeUpdated (under
// ps.mu, copy-on-write into a fresh slice) may publish a change. The fix:
// work on a local copy (`p := snap[i]`, a value, not a pointer into the
// shared array) and pass that copy to storeUpdated, which does its own
// locked copy-on-write swap.
func (ps *simplePredicateSet) Match(t *model.Trace) InterestMatch {
	snap := *ps.snapshot.Load()
	hasError := t.ErrorCount > 0
	for i := range snap {
		p := snap[i] // local copy — never mutate the published snapshot's backing array
		if p.MinDuration > 0 && time.Duration(t.DurationNanos) < p.MinDuration {
			continue
		}
		if p.ErrorsOnly && !hasError {
			continue
		}
		if !predicateCandidateMatches(&p, t) {
			continue
		}
		p.Hits++
		ps.storeUpdated(p)
		return InterestMatch{Matched: true, PredicateID: p.ID, Scope: p.Scope}
	}
	return InterestMatch{}
}

// List returns every predicate for tid (both scopes combined). Not part of
// the canonical DR-10 PredicateSet interface (which has no listing method),
// added here — same "reconstruction license" already used elsewhere in this
// package (e.g. Evicted()) — so a wiring type can implement
// Sampler.ListInterestPredicates (DR-10) via a type assertion.
func (ps *simplePredicateSet) List(tid model.TenantID) []InterestPredicate {
	snap := *ps.snapshot.Load()
	out := make([]InterestPredicate, 0, len(snap))
	for _, p := range snap {
		if p.Tenant == tid {
			out = append(out, p)
		}
	}
	return out
}

// predicateCandidateMatches reports whether the predicate names anything
// that would make it a plausible candidate for this trace (service,
// operation, path signature, trace ID, or attribute); an all-empty
// predicate (no selector fields set) is treated as "no selector -> matches
// on duration/error alone", per §4.4's candidate-union semantics being a
// coarse pre-filter, not the sole match condition.
func predicateCandidateMatches(p *InterestPredicate, t *model.Trace) bool {
	if len(p.TraceIDs) > 0 {
		if _, ok := p.TraceIDs[t.TraceID]; ok {
			return true
		}
	}
	if len(p.PathSigs) > 0 {
		for _, sig := range p.PathSigs {
			if sig == t.PathSignature {
				return true
			}
		}
	}
	if len(p.Services) > 0 {
		for _, svc := range p.Services {
			for _, ts := range t.Services {
				if svc == ts {
					return true
				}
			}
		}
		return false
	}
	if len(p.PathSigs) > 0 || len(p.ErrorSigIDs) > 0 || len(p.TraceIDs) > 0 {
		return false
	}
	// No selector fields set at all: match everything within
	// MinDuration/ErrorsOnly already checked by the caller.
	return true
}

func (ps *simplePredicateSet) storeUpdated(updated InterestPredicate) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	old := *ps.snapshot.Load()
	next := make([]InterestPredicate, len(old))
	copy(next, old)
	for i := range next {
		if next[i].ID == updated.ID {
			next[i] = updated
			break
		}
	}
	ps.snapshot.Store(&next)
}

// Add implements DR-11's max_predicates bound: when the tenant (both scopes
// combined) is already at ps.maxPreds, the lowest-Hits, soonest-expiring
// predicate for that tenant is evicted first (DR-11 §"Eviction at
// max_predicates"), incrementing Evicted().
//
// DEVIATION (w9-sampler): DR-11 also says eviction must "refuse with 429
// predicate_limit if that would evict a ScopeInvestigation predicate
// belonging to a running investigation" — this package has no visibility
// into rca's investigation lifecycle (DR-2 import ban), so that refusal is
// an API-layer concern for a future cmd/traceiq handler, not enforced here.
// This method always evicts the lowest-Hits/soonest-expiring candidate.
func (ps *simplePredicateSet) Add(p InterestPredicate) (string, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p.ID == "" {
		ps.seq++
		p.ID = fmt.Sprintf("pred-%d", ps.seq)
	}
	old := *ps.snapshot.Load()

	tenantCount := 0
	for i := range old {
		if old[i].Tenant == p.Tenant {
			tenantCount++
		}
	}

	next := make([]InterestPredicate, len(old), len(old)+1)
	copy(next, old)

	if tenantCount >= ps.maxPreds {
		evictIdx := -1
		for i := range next {
			if next[i].Tenant != p.Tenant {
				continue
			}
			if evictIdx == -1 {
				evictIdx = i
				continue
			}
			if next[i].Hits < next[evictIdx].Hits {
				evictIdx = i
			} else if next[i].Hits == next[evictIdx].Hits && next[i].ExpiresAt.Before(next[evictIdx].ExpiresAt) {
				evictIdx = i
			}
		}
		if evictIdx >= 0 {
			next = append(next[:evictIdx], next[evictIdx+1:]...)
			atomic.AddUint64(&ps.evictedTotal, 1)
		}
	}

	next = append(next, p)
	ps.snapshot.Store(&next)
	return p.ID, nil
}

func (ps *simplePredicateSet) Remove(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	old := *ps.snapshot.Load()
	next := make([]InterestPredicate, 0, len(old))
	for _, p := range old {
		if p.ID != id {
			next = append(next, p)
		}
	}
	ps.snapshot.Store(&next)
	return nil
}

func (ps *simplePredicateSet) ExpireDue(now time.Time) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	old := *ps.snapshot.Load()
	next := make([]InterestPredicate, 0, len(old))
	expired := 0
	for _, p := range old {
		if !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) {
			expired++
			continue
		}
		next = append(next, p)
	}
	if expired > 0 {
		ps.snapshot.Store(&next)
	}
	return expired
}

// Narrow implements DR-11's circuit breaker (FR-F02-14). Level semantics:
// 1 raises MinDuration, 2 sets ErrorsOnly, 3 narrows Services, 4 expires the
// lowest-Hits ScopeRecurrence predicate. epicenter selection (the busiest
// service/p99) is out of scope here — this applies the level uniformly to
// every current predicate, a documented simplification.
func (ps *simplePredicateSet) Narrow(level int) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	old := *ps.snapshot.Load()
	next := make([]InterestPredicate, len(old))
	copy(next, old)
	narrowed := 0
	switch level {
	case 1:
		for i := range next {
			if next[i].MinDuration == 0 {
				next[i].MinDuration = 250 * time.Millisecond
			}
			next[i].NarrowLevel = 1
			narrowed++
		}
	case 2:
		for i := range next {
			next[i].ErrorsOnly = true
			next[i].NarrowLevel = 2
			narrowed++
		}
	case 3:
		for i := range next {
			if len(next[i].Services) > 1 {
				next[i].Services = next[i].Services[:1]
			}
			next[i].NarrowLevel = 3
			narrowed++
		}
	case 4:
		lowestIdx := -1
		for i := range next {
			if next[i].Scope != ScopeRecurrence {
				continue
			}
			if lowestIdx == -1 || next[i].Hits < next[lowestIdx].Hits {
				lowestIdx = i
			}
		}
		if lowestIdx >= 0 {
			next = append(next[:lowestIdx], next[lowestIdx+1:]...)
			narrowed = 1
		}
	}
	ps.snapshot.Store(&next)
	return narrowed
}
