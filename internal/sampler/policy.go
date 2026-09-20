package sampler

import (
	"context"
	"sync"
	"time"

	"traceiq/internal/model"
)

// PolicyConfig carries F02 §4.2's `sampler.policy.*` config keys (DR-10).
// A package-local struct rather than internal/config.Config, per this
// package's DR-2 import ban and the TDD scope of this pass.
type PolicyConfig struct {
	SlowMinDuration             time.Duration // mandatory floor, default 250ms
	RarePathLookback            time.Duration // default 7 days
	RarePathKeepsPerMin         float64       // per-tenant token bucket, default 60
	MaxPathSignatureCardinality int           // default 200000
	FloorTracesPerMinPerService float64       // default 6
	HealthySampleRate           float64       // default 0.01
	MaxKeepRate                 float64       // default 0.25, hard cap
}

func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		SlowMinDuration:             250 * time.Millisecond,
		RarePathLookback:            7 * 24 * time.Hour,
		RarePathKeepsPerMin:         60,
		MaxPathSignatureCardinality: 200000,
		FloorTracesPerMinPerService: 6,
		HealthySampleRate:           0.01,
		MaxKeepRate:                 0.25,
	}
}

type rareKey struct {
	tenant model.TenantID
	sig    uint64
}

type floorKey struct {
	tenant  model.TenantID
	service string
}

// keepEvent is one Evaluate outcome recorded for the rolling-60s
// max_keep_rate window (Governor.Allow / CurrentFloor), and — since
// w10-review's shed-order fix — for the per-class shed-priority accounting
// FR-F02-13 requires. reason is the FINAL decision.Reason (after any shed
// conversion in this same Evaluate call), so a shed event is recorded as
// KeepShed, not as the class it would otherwise have been: shedRank(KeepShed)
// is -1, so shed events never count toward "is this class still being kept"
// below, which is exactly what the priority walk needs.
type keepEvent struct {
	at     time.Time
	keep   bool
	reason model.KeepReason
}

// shedRank orders FR-F02-13's fixed shed sequence:
// Probabilistic -> Floor -> Interest(Recurrence) -> Rare -> Slow. Lower rank
// sheds first (is least protected); Error and an Investigation-scope
// Interest match (never named in the shed order — DR-10's pseudocode
// comments it "Interest(Recurrence only)") return -1, meaning "never
// shed here".
func shedRank(reason model.KeepReason) int {
	switch reason {
	case model.KeepProbabilistic:
		return 0
	case model.KeepFloor:
		return 1
	case model.KeepInterest:
		return 2
	case model.KeepRare:
		return 3
	case model.KeepSlow:
		return 4
	default:
		return -1
	}
}

const numShedRanks = 5

// DefaultPolicy implements both PolicyEvaluator (the six-class keep policy,
// F02 §4.4) and Governor (the rolling-60s max_keep_rate hard cap +
// AdjustFloor-overridden probabilistic floor, DR-10/DR-12). Combining them
// in one type lets internal callers in this package pass `policy` itself as
// the Governor argument to Evaluate.
type DefaultPolicy struct {
	cfg   PolicyConfig
	clock model.Clock

	mu              sync.Mutex
	rareSeen        map[rareKey]time.Time // in-memory rare-path cache; DR-10 bars SQLite reads from Evaluate
	rareCardinality map[model.TenantID]map[uint64]struct{}
	rareBuckets     map[model.TenantID]*tokenBucket
	floorBuckets    map[floorKey]*tokenBucket
	floors          map[model.TenantID]float64 // AdjustFloor overrides, DR-12
	keepHistory     map[model.TenantID][]keepEvent
}

func NewDefaultPolicy(cfg PolicyConfig, clock model.Clock) *DefaultPolicy {
	return &DefaultPolicy{
		cfg:             cfg,
		clock:           clock,
		rareSeen:        make(map[rareKey]time.Time),
		rareCardinality: make(map[model.TenantID]map[uint64]struct{}),
		rareBuckets:     make(map[model.TenantID]*tokenBucket),
		floorBuckets:    make(map[floorKey]*tokenBucket),
		floors:          make(map[model.TenantID]float64),
		keepHistory:     make(map[model.TenantID][]keepEvent),
	}
}

// AdjustFloor implements DR-12's cost-control entry point: cmd/traceiq
// wires store's CostSignal stream into this (see costcontrol.go).
func (p *DefaultPolicy) AdjustFloor(tid model.TenantID, floor float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.floors[tid] = floor
}

func (p *DefaultPolicy) CurrentFloor(tid model.TenantID) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f, ok := p.floors[tid]; ok {
		return f
	}
	return p.cfg.HealthySampleRate
}

// Allow implements Governor's interface (Allow(class) has no tenant
// parameter — a gap in the DR-10 Governor shape this file already had to
// patch once for CurrentFloor, see sampler.go). Evaluate below does not
// call this method: it type-asserts g to *DefaultPolicy and calls the
// tenant-scoped allowForTenant directly, which is the actually-correct,
// per-tenant-isolated check (DR-5). Allow is kept non-panicking, backed by
// an unscoped ("") bucket, purely so *DefaultPolicy satisfies Governor for
// callers that only have the interface, not the concrete type.
func (p *DefaultPolicy) Allow(class model.KeepReason) bool {
	return p.allowForTenant(model.TenantID(""), p.clock.Now())
}

func (p *DefaultPolicy) allowForTenant(tid model.TenantID, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	hist := p.pruneLocked(tid, now)
	if len(hist) == 0 {
		return true
	}
	kept := 0
	for _, e := range hist {
		if e.keep {
			kept++
		}
	}
	rate := float64(kept) / float64(len(hist))
	return rate <= p.cfg.MaxKeepRate
}

func (p *DefaultPolicy) pruneLocked(tid model.TenantID, now time.Time) []keepEvent {
	hist := p.keepHistory[tid]
	cutoff := now.Add(-60 * time.Second)
	i := 0
	for i < len(hist) && hist[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		hist = hist[i:]
	}
	p.keepHistory[tid] = hist
	return hist
}

func (p *DefaultPolicy) recordKeep(tid model.TenantID, now time.Time, keep bool, reason model.KeepReason) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keepHistory[tid] = append(p.keepHistory[tid], keepEvent{at: now, keep: keep, reason: reason})
}

// shouldShedClass implements FR-F02-13/AC-F02-13's fixed shed order over the
// rolling-60s window: a class only starts shedding once every
// lower-ranked (earlier-in-the-order, less protected) class has already
// been fully vacated — i.e. has zero currently-kept (non-shed) instances
// still inside the window. Concretely: Floor is never shed while a
// Probabilistic keep is still standing in the last 60s; Interest(Recurrence)
// is never shed while either Probabilistic or Floor still has a standing
// keep; and so on through Slow, which is therefore the last class touched
// (Error is simply never a candidate here at all — shedRank returns -1 for
// it, same as a non-Recurrence Interest match, filtered by the caller before
// this is ever invoked).
//
// This is evaluated against history alone (not including the candidate
// event itself, mirroring allowForTenant's same convention), so it is a
// streaming approximation, not a global optimum over the whole window —
// consistent with this being "a purely streaming, one-decision-at-a-time
// evaluator" (w9-sampler-cont.md's own framing of why this was deferred).
func (p *DefaultPolicy) shouldShedClass(tid model.TenantID, reason model.KeepReason, now time.Time) bool {
	rank := shedRank(reason)
	if rank < 0 {
		return false
	}
	p.mu.Lock()
	hist := p.pruneLocked(tid, now)
	var stillKept [numShedRanks]bool
	for _, e := range hist {
		if !e.keep {
			continue
		}
		if r := shedRank(e.reason); r >= 0 {
			stillKept[r] = true
		}
	}
	p.mu.Unlock()

	for r := 0; r < rank; r++ {
		if stillKept[r] {
			return false // a lower-ranked (less protected) class hasn't been fully shed yet
		}
	}
	return true
}

// isRareLocked checks (without consuming a token) whether sig would qualify
// as rare for tid at "now": unseen within RarePathLookback and under the
// cardinality cap. Token consumption happens separately in tryTakeRare so a
// trace that ultimately wins on Error doesn't burn a rare-path token.
func (p *DefaultPolicy) isRareCandidate(tid model.TenantID, sig uint64, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	card := p.rareCardinality[tid]
	if card == nil {
		card = make(map[uint64]struct{})
		p.rareCardinality[tid] = card
	}
	if _, tracked := card[sig]; !tracked && len(card) >= p.cfg.MaxPathSignatureCardinality {
		return false // over cap: new signatures answered as healthy (KeepDropped path), FR-F02-3
	}
	last, seen := p.rareSeen[rareKey{tid, sig}]
	if !seen {
		return true
	}
	return now.Sub(last) > p.cfg.RarePathLookback
}

// tryTakeRare consumes a rare-path token and, if successful, marks sig as
// seen (so a subsequent evaluation of the same signature is no longer
// rare) and tracks cardinality.
func (p *DefaultPolicy) tryTakeRare(tid model.TenantID, sig uint64, now time.Time) bool {
	p.mu.Lock()
	bucket, ok := p.rareBuckets[tid]
	if !ok {
		bucket = newTokenBucket(p.cfg.RarePathKeepsPerMin, now.UnixNano())
		p.rareBuckets[tid] = bucket
	}
	p.mu.Unlock()

	if !bucket.Take(now.UnixNano()) {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.rareSeen[rareKey{tid, sig}] = now
	card := p.rareCardinality[tid]
	if card == nil {
		card = make(map[uint64]struct{})
		p.rareCardinality[tid] = card
	}
	card[sig] = struct{}{}
	return true
}

func (p *DefaultPolicy) tryTakeFloor(tid model.TenantID, service string, now time.Time) bool {
	p.mu.Lock()
	key := floorKey{tid, service}
	bucket, ok := p.floorBuckets[key]
	if !ok {
		bucket = newTokenBucket(p.cfg.FloorTracesPerMinPerService, now.UnixNano())
		p.floorBuckets[key] = bucket
	}
	p.mu.Unlock()
	return bucket.Take(now.UnixNano())
}

// probabilistic implements FR-F02-4: hash(trace_id) mod 10000 < floor*10000,
// deterministic per trace_id and independent of process restarts (pure
// function of the ID and the current floor, no mutable state consulted).
func probabilistic(tid model.TraceID, floor float64) bool {
	h := hashTraceID(tid)
	return (h % 10000) < uint64(floor*10000)
}

func reasonBit(r model.KeepReason) uint16 {
	return 1 << uint16(r)
}

// Evaluate implements PolicyEvaluator per F02 §4.4's six-class algorithm.
// t is *model.Trace per the canonical interface signature in sampler.go
// (DR-10, verbatim) — the shard-local assembly buffer is the package-local
// Trace type (assembly.go), converted to model.Trace at finalize.
// See spanutil.go's traceMaxDurationNanos deviation note re: the
// single-KeyBaseline signature vs. the multi-key pseudocode.
func (p *DefaultPolicy) Evaluate(ctx context.Context, tid model.TenantID, t *model.Trace, b KeyBaseline, m InterestMatch, g Governor) Decision {
	now := p.clock.Now()
	d := Decision{Tenant: tid, TraceID: t.TraceID, DecidedAt: now}

	hasError := traceHasError(t.Spans)
	maxDur := traceMaxDurationNanos(t.Spans)
	slowFires := b.Warmed && maxDur > b.P99Nanos && time.Duration(maxDur) > p.cfg.SlowMinDuration
	rareCandidate := p.isRareCandidate(tid, t.PathSignature, now)

	var secondary uint16
	if hasError {
		secondary |= reasonBit(model.KeepError)
	}
	if slowFires {
		secondary |= reasonBit(model.KeepSlow)
	}
	if rareCandidate {
		secondary |= reasonBit(model.KeepRare)
	}
	if m.Matched {
		secondary |= reasonBit(model.KeepInterest)
		d.MatchedPredicateID = m.PredicateID
	}

	switch {
	case hasError:
		d.Keep, d.Reason = true, model.KeepError
	case slowFires:
		d.Keep, d.Reason = true, model.KeepSlow
	case rareCandidate && p.tryTakeRare(tid, t.PathSignature, now):
		d.Keep, d.Reason = true, model.KeepRare
	case m.Matched:
		d.Keep, d.Reason = true, model.KeepInterest
	case p.tryTakeFloor(tid, t.RootService, now):
		d.Keep, d.Reason = true, model.KeepFloor
	case probabilistic(t.TraceID, g.CurrentFloor(tid)):
		d.Keep, d.Reason = true, model.KeepProbabilistic
	default:
		d.Keep, d.Reason = false, model.KeepDropped
	}

	if t.Truncated && d.Keep {
		d.Reason = model.KeepTruncated
	}

	secondary &^= reasonBit(d.Reason)
	d.SecondaryReasons = secondary

	// max_keep_rate hard cap (FR-F02-13): applied after all six classes,
	// shed in the fixed order Probabilistic -> Floor -> Interest(Recurrence)
	// -> Rare -> Slow; Error (and an Investigation-scope Interest match,
	// shedRank == -1 for both) is never shed.
	//
	// FIXED (w10-review, AC-F02-13): this previously shed ANY non-Error kept
	// decision uniformly the instant the rolling window went over cap —
	// whichever class happened to be evaluated next lost equally, with no
	// regard to FR-F02-13's mandated priority. That defeated the entire
	// point of the six-class system: under sustained overload, a Slow or
	// Rare trace (meant to be protected almost as strongly as Error) had the
	// same shed odds as a Probabilistic one (meant to be cut first). Now
	// shouldShedClass only sheds a class once every less-protected class
	// ahead of it in the order has already been fully vacated in the window.
	if d.Keep && d.Reason != model.KeepError {
		shedEligible := true
		if d.Reason == model.KeepInterest && m.Scope != ScopeRecurrence {
			// DR-10's pseudocode names only "Interest(Recurrence only)" in
			// the shed order — a live ScopeInvestigation predicate match is
			// protected the same as Error.
			shedEligible = false
		}
		if shedEligible {
			if dp, ok := g.(*DefaultPolicy); ok {
				if !dp.allowForTenant(tid, now) && dp.shouldShedClass(tid, d.Reason, now) {
					d.Keep, d.Reason = false, model.KeepShed
				}
			}
		}
	}

	p.recordKeep(tid, now, d.Keep, d.Reason)
	return d
}
