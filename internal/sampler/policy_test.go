package sampler

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"traceiq/internal/model"
)

// TestEvaluate_ErrorAlwaysKept covers F02 §4.4 class 1 / AC-F02-3: any trace
// carrying an error span is kept with Reason == KeepError.
func TestEvaluate_ErrorAlwaysKept(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	tid := model.TenantID("t1")

	span := mkSpan(mkTraceID(1), mkSpanID(1), model.SpanID{}, "svc-a", "op-a", 0, uint64(10*time.Millisecond), true)
	trace := &model.Trace{TraceID: mkTraceID(1), RootService: "svc-a", RootOperation: "op-a", Spans: []model.Span{span}, PathSignature: 111}

	d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, InterestMatch{}, policy)
	if !d.Keep || d.Reason != model.KeepError {
		t.Fatalf("error trace not kept as KeepError: %+v", d)
	}
}

// TestEvaluate_ErrorNeverShedByCap covers DR-10/AC-F02-13's "Error is never
// shed" clause: even with max_keep_rate pinned at 0 (so every other class
// would be shed), an error trace must still come back Keep=true/KeepError.
func TestEvaluate_ErrorNeverShedByCap(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.MaxKeepRate = 0.0
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t1")

	// Prime the rolling-60s window with kept history so allowForTenant sees
	// a rate above the (zero) cap. Recorded as KeepProbabilistic (an
	// arbitrary shed-eligible class) so this history doesn't itself block
	// shouldShedClass's priority walk for the Error decision under test
	// (Error is never even passed through shouldShedClass, but the history
	// must still resemble real prior decisions).
	for i := 0; i < 5; i++ {
		policy.recordKeep(tid, clock.now, true, model.KeepProbabilistic)
	}

	span := mkSpan(mkTraceID(2), mkSpanID(1), model.SpanID{}, "svc-a", "op-a", 0, uint64(time.Millisecond), true)
	trace := &model.Trace{TraceID: mkTraceID(2), RootService: "svc-a", Spans: []model.Span{span}, PathSignature: 222}
	d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, InterestMatch{}, policy)
	if !d.Keep || d.Reason != model.KeepError {
		t.Fatalf("error trace was shed under a saturated cap: %+v", d)
	}
}

// TestEvaluate_HealthyKeptAtConfiguredFloorRate covers AC-F02-4: at
// healthy_sample_rate=0.01 over 1,000,000 trace IDs, the keep rate must land
// within 0.9%-1.1%, and repeat evaluation of the same IDs against a fresh
// policy must reproduce the identical keep/drop decision for every ID
// (hash(trace_id) mod 10000 < floor*10000 is a pure function of the ID).
//
// Rare-path and per-service-floor keeps are disabled via config (set to 0)
// so every kept trace in this test can only have won via the Probabilistic
// class, isolating FR-F02-4 from the other five keep classes.
func TestEvaluate_HealthyKeptAtConfiguredFloorRate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(5000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.RarePathKeepsPerMin = 0
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0.01
	cfg.MaxKeepRate = 1.0 // do not let the unrelated hard cap interfere
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-health")

	const n = 1_000_000
	rng := rand.New(rand.NewSource(42))
	ids := make([]model.TraceID, n)
	keptSet := make([]bool, n)
	kept := 0

	for i := 0; i < n; i++ {
		var id model.TraceID
		rng.Read(id[:])
		ids[i] = id

		span := mkSpan(id, mkSpanID(1), model.SpanID{}, "svc-health", "op-health", 0, uint64(time.Millisecond), false)
		trace := &model.Trace{TraceID: id, RootService: "svc-health", Spans: []model.Span{span}, PathSignature: 999}
		d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, InterestMatch{}, policy)
		if d.Keep {
			kept++
			keptSet[i] = true
			if d.Reason != model.KeepProbabilistic {
				t.Fatalf("healthy trace kept for an unexpected reason %v (want KeepProbabilistic; rare/floor were disabled)", d.Reason)
			}
		}
	}

	rate := float64(kept) / float64(n)
	if rate < 0.009 || rate > 0.011 {
		t.Fatalf("healthy keep rate = %.5f over %d traces, want within [0.009, 0.011] of configured floor 0.01", rate, n)
	}

	// Determinism / bit-identical repeat runs: re-evaluate a subset of the
	// same trace IDs against a fresh policy instance and confirm identical
	// keep decisions.
	policy2 := NewDefaultPolicy(cfg, clock)
	const sample = 20000
	for i := 0; i < sample; i++ {
		id := ids[i]
		span := mkSpan(id, mkSpanID(1), model.SpanID{}, "svc-health", "op-health", 0, uint64(time.Millisecond), false)
		trace := &model.Trace{TraceID: id, RootService: "svc-health", Spans: []model.Span{span}, PathSignature: 999}
		d := policy2.Evaluate(context.Background(), tid, trace, KeyBaseline{}, InterestMatch{}, policy2)
		if d.Keep != keptSet[i] {
			t.Fatalf("keep decision for trace %x not repeatable across a fresh policy instance: first run=%v, rerun=%v", id, keptSet[i], d.Keep)
		}
	}
}

// TestEvaluate_RarePathKeptOnceThenDownsampled covers F02 §4.4 class 3 /
// AC-F02-3(rare)/DR-10: the first trace over an unseen path signature is
// kept as KeepRare; a second trace over the SAME signature, evaluated before
// rare_path_lookback_days has elapsed, is no longer treated as rare (the
// signature was just marked seen) and must fall through to KeepDropped —
// floor and probabilistic keeps are disabled via config so the fallthrough
// is unambiguous.
func TestEvaluate_RarePathKeptOnceThenDownsampled(t *testing.T) {
	clock := &fakeClock{now: time.Unix(9000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-rare")

	const sig = uint64(0xABCDEF)
	mkT := func(n uint64) *model.Trace {
		id := mkTraceID(n)
		span := mkSpan(id, mkSpanID(1), model.SpanID{}, "svc-rare", "op-rare", 0, uint64(time.Millisecond), false)
		return &model.Trace{TraceID: id, RootService: "svc-rare", Spans: []model.Span{span}, PathSignature: sig}
	}

	d1 := policy.Evaluate(context.Background(), tid, mkT(1), KeyBaseline{}, InterestMatch{}, policy)
	if !d1.Keep || d1.Reason != model.KeepRare {
		t.Fatalf("first occurrence of an unseen path signature not kept as KeepRare: %+v", d1)
	}

	d2 := policy.Evaluate(context.Background(), tid, mkT(2), KeyBaseline{}, InterestMatch{}, policy)
	if d2.Keep || d2.Reason != model.KeepDropped {
		t.Fatalf("repeat trace on a just-seen path signature should be downsampled to KeepDropped, got %+v", d2)
	}
}

// TestEvaluate_RarePathRespectsTokenBucket covers FR-F02-3's per-tenant
// rare_path_keeps_per_min token bucket: once the bucket is exhausted, even a
// genuinely never-seen path signature is not kept as rare.
func TestEvaluate_RarePathRespectsTokenBucket(t *testing.T) {
	clock := &fakeClock{now: time.Unix(9000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.RarePathKeepsPerMin = 2
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0
	cfg.MaxKeepRate = 1.0 // isolate the rare-path bucket from the unrelated hard cap
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-rare-bucket")

	mkT := func(n, sig uint64) *model.Trace {
		id := mkTraceID(n)
		span := mkSpan(id, mkSpanID(1), model.SpanID{}, "svc-rare", "op-rare", 0, uint64(time.Millisecond), false)
		return &model.Trace{TraceID: id, RootService: "svc-rare", Spans: []model.Span{span}, PathSignature: sig}
	}

	rareKeeps := 0
	for i := uint64(0); i < 5; i++ {
		// A distinct signature every time, so isRareCandidate is always true;
		// only the token bucket can be limiting.
		d := policy.Evaluate(context.Background(), tid, mkT(i+1, i+1), KeyBaseline{}, InterestMatch{}, policy)
		if d.Keep && d.Reason == model.KeepRare {
			rareKeeps++
		}
	}
	if rareKeeps != int(cfg.RarePathKeepsPerMin) {
		t.Fatalf("rare-path token bucket allowed %d keeps, want exactly the configured capacity %d", rareKeeps, int(cfg.RarePathKeepsPerMin))
	}
}

// TestAdjustFloor_ChangesEffectiveFloor covers DR-12's cost-control entry
// point: AdjustFloor overrides CurrentFloor per-tenant, without affecting
// other tenants, and without a call falling back to the config default.
func TestAdjustFloor_ChangesEffectiveFloor(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	cfg := DefaultPolicyConfig()
	cfg.HealthySampleRate = 0.01
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-cost")

	if got := policy.CurrentFloor(tid); got != 0.01 {
		t.Fatalf("CurrentFloor before any AdjustFloor call = %v, want config default 0.01", got)
	}

	policy.AdjustFloor(tid, 0.05)
	if got := policy.CurrentFloor(tid); got != 0.05 {
		t.Fatalf("CurrentFloor after AdjustFloor(0.05) = %v, want 0.05", got)
	}

	if got := policy.CurrentFloor(model.TenantID("other-tenant")); got != 0.01 {
		t.Fatalf("AdjustFloor for one tenant leaked into another tenant's CurrentFloor: got %v", got)
	}

	// The adjusted floor is what Evaluate's probabilistic class actually
	// uses (g.CurrentFloor(tid) in the switch statement), not just a
	// bookkeeping value nobody reads: raising the floor to 1.0 must keep
	// every trace probabilistically.
	policy.AdjustFloor(tid, 1.0)
	span := mkSpan(mkTraceID(77), mkSpanID(1), model.SpanID{}, "svc-cost", "op-cost", 0, uint64(time.Millisecond), false)
	trace := &model.Trace{TraceID: mkTraceID(77), RootService: "svc-cost", Spans: []model.Span{span}, PathSignature: 555}
	// Disable the higher-priority classes so only Probabilistic can win.
	cfg2 := cfg
	cfg2.RarePathKeepsPerMin = 0
	cfg2.FloorTracesPerMinPerService = 0
	p2 := NewDefaultPolicy(cfg2, clock)
	p2.AdjustFloor(tid, 1.0)
	d := p2.Evaluate(context.Background(), tid, trace, KeyBaseline{}, InterestMatch{}, p2)
	if !d.Keep || d.Reason != model.KeepProbabilistic {
		t.Fatalf("AdjustFloor(tid, 1.0) did not force a probabilistic keep: %+v", d)
	}
}
