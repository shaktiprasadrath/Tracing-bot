package sampler

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// TestPredicateSet_MatchForcesInterestKeep covers DR-11's core contract: a
// predicate whose selector matches a trace returns Matched=true with the
// predicate's ID, and its Hits counter increments.
func TestPredicateSet_MatchForcesInterestKeep(t *testing.T) {
	ps := NewPredicateSet()
	tid := model.TenantID("t1")
	now := time.Unix(1000, 0)

	id, err := ps.Add(InterestPredicate{
		Tenant:    tid,
		Scope:     ScopeInvestigation,
		Source:    "rca:inv-1",
		Services:  []string{"checkout"},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	trace := &model.Trace{TraceID: mkTraceID(1), RootService: "checkout", Services: []string{"checkout"}}
	match := ps.Match(trace)
	if !match.Matched || match.PredicateID != id || match.Scope != ScopeInvestigation {
		t.Fatalf("Match did not fire for a matching predicate: %+v", match)
	}

	nonMatching := &model.Trace{TraceID: mkTraceID(2), RootService: "billing", Services: []string{"billing"}}
	if m := ps.Match(nonMatching); m.Matched {
		t.Fatalf("Match fired for a trace whose service is not in the predicate's Services list: %+v", m)
	}

	sp := ps.(*simplePredicateSet)
	snap := *sp.snapshot.Load()
	if len(snap) != 1 || snap[0].Hits != 1 {
		t.Fatalf("predicate Hits not incremented by the matching call: %+v", snap)
	}
}

// TestEvaluate_InterestMatchKeepsTrace covers DR-11's "Hits is accurate...
// Decision.MatchedPredicateID is set... whenever a predicate matched" wired
// through PolicyEvaluator: when Rare/Floor/Probabilistic are all disabled, a
// matched InterestMatch alone must drive Reason=KeepInterest.
func TestEvaluate_InterestMatchKeepsTrace(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	cfg := DefaultPolicyConfig()
	cfg.RarePathKeepsPerMin = 0
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t1")

	span := mkSpan(mkTraceID(5), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)
	trace := &model.Trace{TraceID: mkTraceID(5), RootService: "svc", Spans: []model.Span{span}, PathSignature: 777}
	match := InterestMatch{Matched: true, PredicateID: "pred-1", Scope: ScopeInvestigation}

	d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, match, policy)
	if !d.Keep || d.Reason != model.KeepInterest || d.MatchedPredicateID != "pred-1" {
		t.Fatalf("interest match did not force a KeepInterest decision: %+v", d)
	}
}

// TestEvaluate_ErrorAndInterestBothRecorded covers AC-F02-16: a trace that
// both errors and matches an open predicate must record Reason=KeepError
// (error wins precedence) while MatchedPredicateID is still set and
// SecondaryReasons still carries the Interest bit.
func TestEvaluate_ErrorAndInterestBothRecorded(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	tid := model.TenantID("t1")

	span := mkSpan(mkTraceID(6), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), true)
	trace := &model.Trace{TraceID: mkTraceID(6), RootService: "svc", Spans: []model.Span{span}, PathSignature: 888}
	match := InterestMatch{Matched: true, PredicateID: "pred-9", Scope: ScopeInvestigation}

	d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, match, policy)
	if !d.Keep || d.Reason != model.KeepError {
		t.Fatalf("error+interest trace did not record Reason=KeepError: %+v", d)
	}
	if d.MatchedPredicateID != "pred-9" {
		t.Fatalf("MatchedPredicateID not recorded on an error-reason decision: %+v", d)
	}
	if d.SecondaryReasons&(1<<uint16(model.KeepInterest)) == 0 {
		t.Fatalf("SecondaryReasons missing the KeepInterest bit: %+v", d)
	}
}

// TestPredicateSet_ExpireDueRemovesByTTL covers DR-11's TTL removal path
// (scope_ttl / recurrence_ttl): a predicate whose ExpiresAt has passed is
// removed by ExpireDue; a zero ExpiresAt never expires.
func TestPredicateSet_ExpireDueRemovesByTTL(t *testing.T) {
	ps := NewPredicateSet()
	now := time.Unix(1000, 0)

	idExpired, _ := ps.Add(InterestPredicate{Tenant: "t1", ExpiresAt: now.Add(-1 * time.Second)})
	idAlive, _ := ps.Add(InterestPredicate{Tenant: "t1", ExpiresAt: now.Add(1 * time.Hour)})
	idNoTTL, _ := ps.Add(InterestPredicate{Tenant: "t1"})

	expired := ps.ExpireDue(now)
	if expired != 1 {
		t.Fatalf("ExpireDue removed %d predicates, want exactly 1", expired)
	}

	sp := ps.(*simplePredicateSet)
	snap := *sp.snapshot.Load()
	remaining := map[string]bool{}
	for _, p := range snap {
		remaining[p.ID] = true
	}
	if remaining[idExpired] {
		t.Fatalf("expired predicate %s still present after ExpireDue: %+v", idExpired, snap)
	}
	if !remaining[idAlive] {
		t.Fatalf("non-expired predicate %s was incorrectly removed: %+v", idAlive, snap)
	}
	if !remaining[idNoTTL] {
		t.Fatalf("zero-ExpiresAt predicate %s was incorrectly removed: %+v", idNoTTL, snap)
	}
}

// TestPredicateSet_MaxPredicatesBoundEvictsLowestHitsSoonestExpiring covers
// DR-11's "Eviction at max_predicates: evict the lowest-Hits,
// soonest-expiring predicate" rule, scoped per tenant.
func TestPredicateSet_MaxPredicatesBoundEvictsLowestHitsSoonestExpiring(t *testing.T) {
	ps := NewPredicateSetWithLimit(3)
	tid := model.TenantID("t1")
	base := time.Unix(1000, 0)

	idA, _ := ps.Add(InterestPredicate{Tenant: tid, ExpiresAt: base.Add(10 * time.Minute)})
	idB, _ := ps.Add(InterestPredicate{Tenant: tid, ExpiresAt: base.Add(20 * time.Minute)})
	idC, _ := ps.Add(InterestPredicate{Tenant: tid, ExpiresAt: base.Add(30 * time.Minute)})

	sp := ps.(*simplePredicateSet)
	if got := len(*sp.snapshot.Load()); got != 3 {
		t.Fatalf("expected 3 predicates before hitting the bound, got %d", got)
	}

	// All three have Hits=0; idA expires soonest, so it is the eviction
	// candidate when a 4th predicate is added for the same tenant.
	idD, err := ps.Add(InterestPredicate{Tenant: tid, ExpiresAt: base.Add(40 * time.Minute)})
	if err != nil {
		t.Fatalf("Add at the bound: %v", err)
	}

	snap := *sp.snapshot.Load()
	if len(snap) != 3 {
		t.Fatalf("max_predicates bound not enforced: have %d predicates for the tenant, want <= 3: %+v", len(snap), snap)
	}
	present := map[string]bool{}
	for _, p := range snap {
		present[p.ID] = true
	}
	if present[idA] {
		t.Fatalf("soonest-expiring, lowest-Hits predicate %s was not evicted: %+v", idA, snap)
	}
	if !present[idB] || !present[idC] || !present[idD] {
		t.Fatalf("wrong predicate evicted, want B/C/D to remain: %+v", snap)
	}
	if got := sp.Evicted(); got != 1 {
		t.Fatalf("Evicted() = %d, want 1", got)
	}

	// A different tenant's predicates are a separate bound (max_predicates
	// is per-tenant, both scopes combined).
	if _, err := ps.Add(InterestPredicate{Tenant: model.TenantID("other"), ExpiresAt: base}); err != nil {
		t.Fatalf("Add for a different tenant: %v", err)
	}
	if got := len(*sp.snapshot.Load()); got != 4 {
		t.Fatalf("max_predicates bound leaked across tenants: %d total predicates, want 4 (3 for t1 + 1 for other)", got)
	}
}

// TestPredicateSet_MaxPredicatesBoundPrefersHitsOverExpiry covers the
// tie-break precedence: a predicate with fewer Hits is evicted before one
// with more Hits, even if the low-Hits predicate expires later.
func TestPredicateSet_MaxPredicatesBoundPrefersHitsOverExpiry(t *testing.T) {
	ps := NewPredicateSetWithLimit(2)
	tid := model.TenantID("t1")
	base := time.Unix(1000, 0)

	// idLowHits expires LATER but will be matched (Hits>0) before the bound
	// is hit; idHighHitsButSoonExpiry starts with Hits=0 too but we bump its
	// Hits via Match. Then idLowHits (still Hits=0, expires soonest of the
	// two zero-Hits predicates) should be the eviction target.
	idKeepMe, _ := ps.Add(InterestPredicate{Tenant: tid, Services: []string{"svc-keep"}, ExpiresAt: base.Add(5 * time.Minute)})
	idEvictMe, _ := ps.Add(InterestPredicate{Tenant: tid, Services: []string{"svc-evict"}, ExpiresAt: base.Add(time.Hour)})

	// Give idKeepMe a Hit so it is no longer the lowest-Hits predicate,
	// despite expiring sooner than idEvictMe.
	ps.Match(&model.Trace{TraceID: mkTraceID(1), Services: []string{"svc-keep"}})

	idNew, err := ps.Add(InterestPredicate{Tenant: tid, ExpiresAt: base.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("Add at the bound: %v", err)
	}

	sp := ps.(*simplePredicateSet)
	snap := *sp.snapshot.Load()
	present := map[string]bool{}
	for _, p := range snap {
		present[p.ID] = true
	}
	if present[idEvictMe] {
		t.Fatalf("lowest-Hits predicate %s was not evicted despite a higher-Hits, sooner-expiring predicate being present: %+v", idEvictMe, snap)
	}
	if !present[idKeepMe] || !present[idNew] {
		t.Fatalf("wrong predicate evicted: %+v", snap)
	}
}
