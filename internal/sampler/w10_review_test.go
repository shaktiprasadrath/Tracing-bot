package sampler

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// TestEvaluate_ShedOrder_ProbabilisticShedBeforeSlow covers AC-F02-13's fixed
// shed order at its most direct: with the rolling-60s window saturated with
// Probabilistic keeps (rank 0, least protected) still standing, a
// newly-evaluated Slow decision (rank 4, most protected short of Error) MUST
// NOT be shed — Probabilistic must be shed first. This is the exact gap
// w9-sampler-cont.md flagged: "current cap sheds any non-Error class
// uniformly" would have shed the Slow trace here too.
func TestEvaluate_ShedOrder_ProbabilisticShedBeforeSlow(t *testing.T) {
	clock := &fakeClock{now: time.Unix(20000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.MaxKeepRate = 0.1 // low cap, easy to saturate
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-shed")

	// Saturate the window with Probabilistic keeps (rank 0) so the rolling
	// rate is already over cap, and Probabilistic has standing kept entries.
	for i := 0; i < 10; i++ {
		policy.recordKeep(tid, clock.now, true, model.KeepProbabilistic)
	}
	// A few decided-but-dropped entries too, so allowForTenant's rate
	// computation isn't vacuously 100% kept either way — still over cfg.MaxKeepRate.
	for i := 0; i < 2; i++ {
		policy.recordKeep(tid, clock.now, false, model.KeepDropped)
	}

	if policy.allowForTenant(tid, clock.now) {
		t.Fatalf("test setup invalid: rolling window not over cap")
	}

	// A Slow-class decision arrives next: baseline warmed, over P99 and over
	// the mandatory floor, no error. It must survive despite the cap being
	// breached, because Probabilistic keeps are still standing in the window.
	span := mkSpan(mkTraceID(500), mkSpanID(1), model.SpanID{}, "svc-slow", "op-slow", 0, uint64(500*time.Millisecond), false)
	trace := &model.Trace{TraceID: mkTraceID(500), RootService: "svc-slow", Spans: []model.Span{span}, PathSignature: 0xDEAD}
	baseline := KeyBaseline{Warmed: true, P99Nanos: uint64(100 * time.Millisecond)}

	d := policy.Evaluate(context.Background(), tid, trace, baseline, InterestMatch{}, policy)
	if !d.Keep || d.Reason != model.KeepSlow {
		t.Fatalf("Slow decision was shed ahead of standing Probabilistic keeps (violates AC-F02-13's fixed order): %+v", d)
	}
}

// TestEvaluate_ShedOrder_SlowShedsOnceLowerRanksVacated is the flip side:
// once every lower-ranked class (Probabilistic/Floor/Interest(Recurrence)/
// Rare) has been fully shed out of the rolling window (i.e. has zero
// standing kept entries), a Slow decision becomes shed-eligible too — Slow
// is protected relative to the other five classes, not literally unshedable
// like Error.
func TestEvaluate_ShedOrder_SlowShedsOnceLowerRanksVacated(t *testing.T) {
	clock := &fakeClock{now: time.Unix(21000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.MaxKeepRate = 0.1
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-shed2")

	// Window saturated with KeepError kept=true history (Error inflates the
	// raw rolling rate over cap, same as it would in production, but
	// shedRank(KeepError) == -1 so it never counts as a "standing kept"
	// instance of any of the five shed-eligible classes) plus KeepShed
	// history (every lower-ranked class has already been shed out — none
	// are "standing kept").
	for i := 0; i < 10; i++ {
		policy.recordKeep(tid, clock.now, true, model.KeepError)
	}
	for i := 0; i < 10; i++ {
		policy.recordKeep(tid, clock.now, false, model.KeepShed)
	}
	if policy.allowForTenant(tid, clock.now) {
		t.Fatalf("test setup invalid: rolling window not over cap")
	}

	span := mkSpan(mkTraceID(501), mkSpanID(1), model.SpanID{}, "svc-slow2", "op-slow2", 0, uint64(500*time.Millisecond), false)
	trace := &model.Trace{TraceID: mkTraceID(501), RootService: "svc-slow2", Spans: []model.Span{span}, PathSignature: 0xBEEF}
	baseline := KeyBaseline{Warmed: true, P99Nanos: uint64(100 * time.Millisecond)}

	d := policy.Evaluate(context.Background(), tid, trace, baseline, InterestMatch{}, policy)
	if d.Keep || d.Reason != model.KeepShed {
		t.Fatalf("Slow decision should be shed-eligible once all lower-ranked classes are vacated: %+v", d)
	}
}

// TestEvaluate_ShedOrder_InvestigationScopeInterestNeverShed covers the
// pseudocode's "Interest(Recurrence only)" qualifier: a ScopeInvestigation
// predicate match must survive the cap exactly like Error, never entering
// the shed order at all.
func TestEvaluate_ShedOrder_InvestigationScopeInterestNeverShed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(22000, 0)}
	cfg := DefaultPolicyConfig()
	cfg.MaxKeepRate = 0.0 // saturate immediately
	// Isolate Interest from the higher-precedence Rare/Floor classes (F02
	// §4.4's decision order is Error > Slow > Rare > Interest > Floor >
	// Probabilistic), same pattern as TestEvaluate_InterestMatchKeepsTrace.
	cfg.RarePathKeepsPerMin = 0
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0
	policy := NewDefaultPolicy(cfg, clock)
	tid := model.TenantID("t-inv")

	for i := 0; i < 5; i++ {
		policy.recordKeep(tid, clock.now, true, model.KeepProbabilistic)
	}

	span := mkSpan(mkTraceID(502), mkSpanID(1), model.SpanID{}, "svc-inv", "op-inv", 0, uint64(time.Millisecond), false)
	trace := &model.Trace{TraceID: mkTraceID(502), RootService: "svc-inv", Spans: []model.Span{span}, PathSignature: 0xF00D}
	match := InterestMatch{Matched: true, PredicateID: "pred-inv", Scope: ScopeInvestigation}

	d := policy.Evaluate(context.Background(), tid, trace, KeyBaseline{}, match, policy)
	if !d.Keep || d.Reason != model.KeepInterest {
		t.Fatalf("ScopeInvestigation interest match was shed, want protected like Error: %+v", d)
	}
}

// TestConsume_DuplicateSpanIDDroppedBeforeAssemblyAndRED covers FR-F02-5(c):
// a duplicate SpanID delivery (F01's at-least-once semantics) must be
// dropped before BOTH assembly (not appended to the trace buffer twice) and
// RED (not double-counted), and counted via DuplicateSpansTotal.
func TestConsume_DuplicateSpanIDDroppedBeforeAssemblyAndRED(t *testing.T) {
	clock := &fakeClock{now: time.Unix(8000, 0)}
	wal := NewMemWAL()
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	preds := NewPredicateSet()
	mgr := NewManager(DefaultAssemblyConfig(), clock, wal, policy, policy, preds, 1)
	tid := model.TenantID("t1")

	id := mkTraceID(900)
	spanID := mkSpanID(1)
	// isError=true so the eventual Decision is deterministically
	// Keep=true/KeepError, making the assembly-side SpanCount assertion
	// below unconditional instead of racing the six-class policy.
	span := mkSpan(id, spanID, model.SpanID{}, "svc-dup", "op-dup", 0, uint64(time.Millisecond), true)

	// Same span delivered twice across two separate Consume batches
	// (at-least-once redelivery).
	if err := mgr.Consume(context.Background(), tid, []model.Span{span}); err != nil {
		t.Fatalf("Consume (first delivery): %v", err)
	}
	if err := mgr.Consume(context.Background(), tid, []model.Span{span}); err != nil {
		t.Fatalf("Consume (duplicate delivery): %v", err)
	}

	if got := mgr.DuplicateSpansTotal(); got != 1 {
		t.Fatalf("DuplicateSpansTotal = %d, want 1", got)
	}

	mgr.DrainRED()
	got := drainRED(mgr)
	sample, ok := got["svc-dup/op-dup"]
	if !ok {
		t.Fatalf("RED sample missing for svc-dup/op-dup: %+v", got)
	}
	if sample.Calls != 1 {
		t.Fatalf("Calls = %d, want 1 (duplicate must not double-count RED)", sample.Calls)
	}

	// Assembly must not have appended the span body twice either: finalize
	// past hard_timeout and check the resulting trace's SpanCount.
	clock.now = clock.now.Add(time.Hour)
	mgr.Tick(context.Background())
	select {
	case d := <-mgr.Decisions():
		if !d.Keep || d.Reason != model.KeepError {
			t.Fatalf("expected a deterministic KeepError decision, got %+v", d)
		}
	default:
		t.Fatalf("expected a decision after hard_timeout")
	}
	select {
	case tr := <-mgr.Traces():
		if tr.SpanCount != 1 {
			t.Fatalf("trace SpanCount = %d, want 1 (duplicate span body must not be assembled twice)", tr.SpanCount)
		}
	default:
		t.Fatalf("expected the kept (KeepError) trace on Traces()")
	}
}

// TestPredicateSet_MatchDoesNotMutatePublishedSnapshot covers the fixed data
// race in Match: the slice returned by an earlier ps.snapshot.Load() call
// (as a caller might hold onto, e.g. across two ExpireDue/Add cycles reading
// stale data intentionally) must never be mutated in place by a later Match
// call incrementing Hits — Hits changes must only ever be visible through a
// fresh Load() after Match's own copy-on-write swap.
func TestPredicateSet_MatchDoesNotMutatePublishedSnapshot(t *testing.T) {
	ps := NewPredicateSet()
	tid := model.TenantID("t1")

	id, err := ps.Add(InterestPredicate{Tenant: tid, Services: []string{"checkout"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	sp := ps.(*simplePredicateSet)
	preSnapshot := *sp.snapshot.Load() // the slice value in effect before Match runs
	if len(preSnapshot) != 1 || preSnapshot[0].Hits != 0 {
		t.Fatalf("unexpected pre-match snapshot: %+v", preSnapshot)
	}

	trace := &model.Trace{TraceID: mkTraceID(1), Services: []string{"checkout"}}
	if m := ps.Match(trace); !m.Matched || m.PredicateID != id {
		t.Fatalf("Match did not fire: %+v", m)
	}

	// The OLD slice header/backing array captured before Match must still
	// read Hits==0: if Match had mutated it in place (the pre-fix bug), this
	// would now read 1, proving the "published snapshot" was not actually
	// immutable.
	if preSnapshot[0].Hits != 0 {
		t.Fatalf("Match mutated a previously-published snapshot in place: pre-snapshot Hits = %d, want 0 (immutable)", preSnapshot[0].Hits)
	}

	// The NEW snapshot (post-Match) must reflect the increment.
	postSnapshot := *sp.snapshot.Load()
	if len(postSnapshot) != 1 || postSnapshot[0].Hits != 1 {
		t.Fatalf("post-match snapshot Hits not incremented: %+v", postSnapshot)
	}
}

// TestImpl_SatisfiesSamplerInterface covers the missing top-level wiring
// flagged in w9-sampler-cont.md: NewImpl must produce a single object
// exposing the FULL DR-10 Sampler interface (Consume + predicate CRUD +
// AdjustFloor + channels + FlushAll/ReplayWAL/Stats), not just Manager's
// subset.
func TestImpl_SatisfiesSamplerInterface(t *testing.T) {
	clock := &fakeClock{now: time.Unix(30000, 0)}
	wal := NewMemWAL()
	var s Sampler = NewImpl(DefaultAssemblyConfig(), DefaultPolicyConfig(), clock, wal, 1)
	tid := model.TenantID("t-impl")
	ctx := context.Background()

	id, err := s.SetInterestPredicate(ctx, tid, InterestPredicate{Scope: ScopeInvestigation, Services: []string{"checkout"}})
	if err != nil || id == "" {
		t.Fatalf("SetInterestPredicate: id=%q err=%v", id, err)
	}

	list, err := s.ListInterestPredicates(ctx, tid)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("ListInterestPredicates = %+v, err=%v, want exactly the predicate just added", list, err)
	}

	if err := s.AdjustFloor(ctx, tid, 0.5); err != nil {
		t.Fatalf("AdjustFloor: %v", err)
	}

	if err := s.RemoveInterestPredicate(ctx, tid, id); err != nil {
		t.Fatalf("RemoveInterestPredicate: %v", err)
	}
	list, err = s.ListInterestPredicates(ctx, tid)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListInterestPredicates after remove = %+v, err=%v, want empty", list, err)
	}

	// Consume/FlushAll/Decisions still work through the embedded Manager.
	spans := []model.Span{mkSpan(mkTraceID(1), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)}
	if err := s.Consume(ctx, tid, spans); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := s.FlushAll(ctx); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	select {
	case <-s.Decisions():
	default:
		t.Fatalf("expected a decision after FlushAll")
	}
}
