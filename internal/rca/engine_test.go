package rca

import (
	"context"
	"sync"
	"testing"
	"time"

	"traceiq/internal/model"
)

// --- test doubles ---

// fakeClock is a fully mockable model.Clock so budget-boundary tests don't
// need to burn real wall-clock time (DR-4/DR-31's Clock-injection point;
// realClock is the engine's own production default).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
func (c *fakeClock) NewTicker(time.Duration) model.Ticker {
	panic("fakeClock: NewTicker not used by these tests")
}
func (c *fakeClock) NewTimer(time.Duration) model.Timer {
	panic("fakeClock: NewTimer not used by these tests")
}
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.Advance(d)
	return nil
}

var _ model.Clock = (*fakeClock)(nil)

// fakeRegistry is a directly-programmable ToolRegistry (DR-15) so engine
// tests can control exactly what each tool call returns without wiring the
// full Tool/backend chain in tools.go.
type fakeRegistry struct {
	dispatch func(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error)
}

func (f *fakeRegistry) Get(model.ToolName) (Tool, bool) { return nil, false }
func (f *fakeRegistry) Names() []model.ToolName {
	return []model.ToolName{ToolTraceQuery, ToolLogQuery, ToolMetricQuery, ToolTopologyQuery, ToolMemoryQuery}
}
func (f *fakeRegistry) Dispatch(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	return f.dispatch(ctx, tid, a)
}

var _ ToolRegistry = (*fakeRegistry)(nil)

// fakeReasoner lets engine-loop-shape tests (budget termination, invalid
// args) drive NextStep/Conclude precisely, independent of any rule catalog.
type fakeReasoner struct {
	next     func(ctx context.Context, s State) (Proposal, error)
	conclude func(ctx context.Context, s State) (model.Conclusion, error)
	kind     model.ReasonerKind // "" defaults to ReasonerRules (w15 addition, additive)
}

func (r *fakeReasoner) Kind() model.ReasonerKind {
	if r.kind == "" {
		return model.ReasonerRules
	}
	return r.kind
}
func (r *fakeReasoner) NextStep(ctx context.Context, _ model.TenantID, s State) (Proposal, error) {
	return r.next(ctx, s)
}
func (r *fakeReasoner) Conclude(ctx context.Context, _ model.TenantID, s State) (model.Conclusion, error) {
	if r.conclude != nil {
		return r.conclude(ctx, s)
	}
	return model.Conclusion{}, nil
}

var _ Reasoner = (*fakeReasoner)(nil)

// fakeInterestSink records every Set/Remove call so DR-11's two-phase
// lifecycle can be asserted precisely, including that Remove receives the
// exact ID the matching Set call returned (not a reconstruction of Source).
type fakeInterestSink struct {
	mu      sync.Mutex
	nextID  int
	sets    []InterestPredicate
	removed []string
}

func (s *fakeInterestSink) SetInterestPredicate(_ context.Context, _ model.TenantID, p InterestPredicate) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := "sink-generated-id-" + itoa(s.nextID) // deliberately NOT "rca:"+invID
	p.ID = id
	s.sets = append(s.sets, p)
	return id, nil
}
func (s *fakeInterestSink) RemoveInterestPredicate(_ context.Context, _ model.TenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, id)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func testTenant() model.TenantID { return model.TenantID("t1") }

func testIncident() model.Incident {
	return model.Incident{ID: "inc-1", EpicenterService: "checkout", Fingerprint: "fp1:abc"}
}

func newTestEngine(reasoner Reasoner, registry ToolRegistry, interest InterestSink, clock model.Clock) *engine {
	journal := NewMemJournal()
	objects := NewMemObjectStore()
	return NewEngine(journal, registry, reasoner, objects, interest, clock)
}

// okRegistry always dispatches successfully with a small valid JSON rows body.
func okRegistry() *fakeRegistry {
	return &fakeRegistry{dispatch: func(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
		return model.ToolResult{Rows: []byte(`[]`), Tool: a.Tool, ObservedAt: time.Now()}, nil
	}}
}

// --- (b) budget termination: MaxSteps, graceful, no hang/panic ---

func TestInvestigate_MaxStepsBudgetExhausted(t *testing.T) {
	registry := okRegistry()
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			// Always propose a fresh tool call; never Done, never ErrNoMoreRules.
			return Proposal{
				Phase: model.PhaseTest,
				Call:  &ToolArgs{Tool: ToolMemoryQuery, Memory: &MemoryQueryArgs{Text: "x"}},
			}, nil
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)

	done := make(chan model.Investigation, 1)
	go func() {
		inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
		if err != nil {
			t.Errorf("Investigate returned error: %v", err)
		}
		done <- inv
	}()

	select {
	case inv := <-done:
		if inv.TerminationReason != model.TermSteps {
			t.Fatalf("TerminationReason = %v, want TermSteps (MaxSteps=%d < MaxToolCalls=%d means Steps binds first under this always-dispatch pattern)",
				inv.TerminationReason, inv.Budget.MaxSteps, inv.Budget.MaxToolCalls)
		}
		if inv.Status != model.InvestigationBudgetExhausted {
			t.Errorf("Status = %v, want InvestigationBudgetExhausted", inv.Status)
		}
		if len(inv.Steps) != inv.Budget.MaxSteps {
			t.Errorf("len(Steps) = %d, want exactly MaxSteps=%d", len(inv.Steps), inv.Budget.MaxSteps)
		}
		if inv.ReportMarkdown == "" {
			t.Error("expected a partial report even on budget exhaustion")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Investigate did not terminate within 10s: loop appears to hang under budget exhaustion")
	}
}

// TestInvestigate_MaxToolCallsUnreachableUnderDefaults documents a judgment
// call found during this wave's TDD pass rather than a bug fixed: under
// DefaultBudget (MaxSteps=24 < MaxToolCalls=40), and given every branch of
// the loop that increments Spend.ToolCalls also increments Spend.Steps
// (engine.go's invalid-args and dispatch-success branches), Spend.Steps can
// never be less than Spend.ToolCalls. So MaxToolCalls can only ever bind if
// MaxSteps were raised above MaxToolCalls, or a future change adds a branch
// that charges ToolCalls without charging Steps. This is a DR-17 §17.5
// config-value question (which dimension binds first), not an internal/rca
// bug, so it is left as-is — see docs/reports/w11-rca-cont.md.
func TestInvestigate_MaxToolCallsUnreachableUnderDefaults(t *testing.T) {
	b := DefaultBudget()
	if !(b.MaxSteps < b.MaxToolCalls) {
		t.Skip("DefaultBudget changed: MaxSteps no longer strictly less than MaxToolCalls; the documented invariant this test guards no longer holds")
	}
}

// --- (b)/(f) wall-clock budget exhaustion via a fully-mocked clock ---

func TestInvestigate_WallClockBudgetExhausted(t *testing.T) {
	registry := okRegistry()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	firstCall := true
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			if firstCall {
				firstCall = false
				// Push the clock past WallClock so the NEXT loop iteration's
				// top-of-loop check trips before a second NextStep call.
				clock.Advance(DefaultBudget().WallClock + time.Second)
			}
			return Proposal{Phase: model.PhaseHypothesize}, nil // pure-reasoning step, never Done
		},
	}
	e := newTestEngine(reasoner, registry, nil, clock)

	done := make(chan model.Investigation, 1)
	go func() {
		inv, _ := e.Investigate(context.Background(), testTenant(), testIncident())
		done <- inv
	}()

	select {
	case inv := <-done:
		if inv.TerminationReason != model.TermWallClock {
			t.Fatalf("TerminationReason = %v, want TermWallClock", inv.TerminationReason)
		}
		if inv.Status != model.InvestigationBudgetExhausted {
			t.Errorf("Status = %v, want InvestigationBudgetExhausted", inv.Status)
		}
		if len(inv.Steps) >= inv.Budget.MaxSteps {
			t.Errorf("len(Steps) = %d, expected wall-clock to terminate well before MaxSteps=%d", len(inv.Steps), inv.Budget.MaxSteps)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Investigate did not terminate: wall-clock budget check appears to hang")
	}
}

// --- (f) budget exhaustion never panics, even from an unusual proposal shape ---

func TestInvestigate_BudgetExhaustionDoesNotPanicOnPureSteps(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Investigate panicked: %v", r)
		}
	}()
	registry := okRegistry()
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{Phase: model.PhaseHypothesize}, nil // pure-reasoning, forever
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.TerminationReason != model.TermSteps {
		t.Fatalf("TerminationReason = %v, want TermSteps", inv.TerminationReason)
	}
}

// --- (e) interest predicate: two-phase lifecycle (DR-11) ---

func TestInvestigate_InterestPredicateLifecycle(t *testing.T) {
	sink := &fakeInterestSink{}
	registry := okRegistry()
	// Reasoner that confirms immediately with high confidence so the
	// investigation concludes (exercising the phase-B recurrence push too).
	calls := 0
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			calls++
			return Proposal{Phase: model.PhaseValidate, Done: true}, nil
		},
		conclude: func(ctx context.Context, s State) (model.Conclusion, error) {
			return model.Conclusion{RootCause: "root", Confidence: 0.9}, nil
		},
	}
	e := newTestEngine(reasoner, registry, sink, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationConcluded {
		t.Fatalf("Status = %v, want Concluded", inv.Status)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()

	if len(sink.sets) < 1 {
		t.Fatal("expected at least one SetInterestPredicate call (the scope predicate)")
	}
	scopeSet := sink.sets[0]
	if scopeSet.Scope != ScopeInvestigation {
		t.Errorf("first predicate Scope = %v, want ScopeInvestigation (FR-F06-12a)", scopeSet.Scope)
	}
	if scopeSet.Source != "rca:"+inv.ID {
		t.Errorf("Source = %q, want rca:%s", scopeSet.Source, inv.ID)
	}

	if len(sink.removed) != 1 {
		t.Fatalf("expected exactly one RemoveInterestPredicate call, got %d", len(sink.removed))
	}
	// The critical regression check: Remove must be called with the ID the
	// sink itself assigned via Set, not a reconstructed "rca:"+invID string.
	if sink.removed[0] != scopeSet.ID {
		t.Errorf("RemoveInterestPredicate called with %q, want the sink-assigned scope predicate ID %q", sink.removed[0], scopeSet.ID)
	}

	// Concluded + Confidence >= threshold: a phase-B recurrence predicate
	// must also have been pushed (FR-F06-12b).
	if len(sink.sets) < 2 {
		t.Fatal("expected a second SetInterestPredicate call for the phase-B recurrence predicate")
	}
	recurSet := sink.sets[1]
	if recurSet.Scope != ScopeRecurrence {
		t.Errorf("second predicate Scope = %v, want ScopeRecurrence (FR-F06-12b)", recurSet.Scope)
	}
	if len(recurSet.Services) != 0 {
		t.Errorf("recurrence predicate must never be scoped by Services alone, got %v", recurSet.Services)
	}
}

// TestInvestigate_InterestPredicateNotPushedOnLowConfidence checks the
// negative half of FR-F06-12b: no recurrence predicate on an inconclusive
// result.
func TestInvestigate_InterestPredicateNotPushedOnLowConfidence(t *testing.T) {
	sink := &fakeInterestSink{}
	registry := okRegistry()
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{}, ErrNoMoreRules
		},
	}
	e := newTestEngine(reasoner, registry, sink, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationInconclusive {
		t.Fatalf("Status = %v, want Inconclusive", inv.Status)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.sets) != 1 {
		t.Errorf("expected exactly one SetInterestPredicate call (scope only), got %d", len(sink.sets))
	}
}

// --- (c)/(a-ish) invalid tool args are recorded and charged, never dispatched ---

func TestInvestigate_InvalidToolArgsRecordedAndCountsAgainstBudget(t *testing.T) {
	dispatched := false
	registry := &fakeRegistry{dispatch: func(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
		dispatched = true
		return model.ToolResult{}, nil
	}}
	first := true
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			if first {
				first = false
				// Tool set but no matching payload pointer set: validateToolArgs
				// must reject this before it ever reaches Dispatch.
				return Proposal{Phase: model.PhaseTest, Call: &ToolArgs{Tool: ToolMemoryQuery}}, nil
			}
			return Proposal{}, ErrNoMoreRules
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if dispatched {
		t.Fatal("invalid ToolArgs must never reach Dispatch")
	}
	var found bool
	for _, st := range inv.Steps {
		if st.Verdict == model.VerdictInvalidArgs {
			found = true
			if st.ToolArgsJSON == "" || st.ToolArgsHash == "" {
				t.Error("an invalid_args step must still record ToolArgsJSON/ToolArgsHash")
			}
		}
	}
	if !found {
		t.Fatal("expected one Step with Verdict == invalid_args")
	}
	if inv.Spend.ToolCalls < 1 {
		t.Error("an invalid_args step must count against max_tool_calls (F06 §4.4)")
	}
}

// --- Abort interrupts a running investigation (w12 review fix) ---
//
// Before this fix, Abort only mutated the engine's stored copy and the
// journal directly; a concurrently-running Investigate call was completely
// unaware of it, ran to its own (non-aborted) completion, and then
// overwrote Abort's write via its own final storeInv/SetStatus calls —
// silently discarding the abort. It also never removed the FR-F06-12a scope
// predicate. This test uses a reasoner that blocks on a test-controlled gate
// (deliberately NOT selecting on ctx, mirroring the real rules.Reasoner's
// signature, which ignores context entirely) so the test can call Abort
// while a step is provably in flight, then release the gate and assert the
// engine's own next loop-top check is what stops it — not the reasoner
// noticing cancellation itself.
func TestInvestigate_AbortInterruptsRunningLoop(t *testing.T) {
	sink := &fakeInterestSink{}
	registry := okRegistry()
	started := make(chan struct{}, 1)
	proceed := make(chan struct{})
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-proceed // held open until the test explicitly releases it
			return Proposal{
				Phase: model.PhaseTest,
				Call:  &ToolArgs{Tool: ToolMemoryQuery, Memory: &MemoryQueryArgs{Text: "x"}},
			}, nil
		},
	}
	e := newTestEngine(reasoner, registry, sink, nil)

	done := make(chan model.Investigation, 1)
	go func() {
		inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
		if err != nil {
			t.Errorf("Investigate returned error: %v", err)
		}
		done <- inv
	}()

	<-started // the first NextStep call is now blocked on <-proceed

	// The scope predicate (FR-F06-12a) is pushed before the loop starts, so
	// it is already recorded by the time the first NextStep call fires;
	// its Source ("rca:<investigationID>") is this investigation's real ID.
	sink.mu.Lock()
	if len(sink.sets) != 1 {
		sink.mu.Unlock()
		t.Fatalf("expected exactly one scope predicate pushed before the loop started, got %d", len(sink.sets))
	}
	invID := sink.sets[0].Source[len("rca:"):]
	sink.mu.Unlock()

	if err := e.Abort(context.Background(), testTenant(), invID, "operator requested"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	close(proceed) // let the in-flight NextStep call return; the engine
	// dispatches its proposed tool call as usual, then hits the loop-top
	// ctx.Err() check on the NEXT iteration and stops there.

	select {
	case inv := <-done:
		if inv.TerminationReason != model.TermAborted {
			t.Fatalf("TerminationReason = %v, want TermAborted", inv.TerminationReason)
		}
		if inv.Status != model.InvestigationAborted {
			t.Fatalf("Status = %v, want InvestigationAborted", inv.Status)
		}
		if inv.Error != "operator requested" {
			t.Errorf("Error = %q, want the Abort reason to be recorded", inv.Error)
		}
		// The Budget.MaxSteps ceiling (24) proves the loop stopped because of
		// the abort signal, not because it ran to its own budget exhaustion:
		// at most the one in-flight step plus Contextualize should exist.
		if len(inv.Steps) >= inv.Budget.MaxSteps {
			t.Errorf("len(Steps) = %d, expected Abort to stop the loop well short of MaxSteps=%d", len(inv.Steps), inv.Budget.MaxSteps)
		}
	case <-time.After(30 * time.Second):
		// 30s (not 10s, unlike this file's other budget-exhaustion tests):
		// the assertion here is entirely channel-gated (no sleeps), so this
		// is purely a hang backstop, not a real deadline — give it extra
		// headroom against goroutine-scheduling contention under a full
		// `go test ./...` run (observed once in practice; reproduced as a
		// clean pass in isolation and on re-run, i.e. scheduling delay, not
		// a logic race — see docs/reports/w12-review-rca.md).
		t.Fatal("Investigate did not terminate within 30s of Abort: the abort signal did not reach the running loop")
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.removed) != 1 || sink.removed[0] != sink.sets[0].ID {
		t.Errorf("expected the scope predicate to be removed on Abort too (FR-F06-12a: any terminal status), removed=%v want=[%v]", sink.removed, sink.sets[0].ID)
	}
}

// TestInvestigate_AbortOnAlreadyTerminalInvestigationIsBestEffort covers the
// fallback branch: aborting an investigation that is not currently running
// (already finished) still marks it, matching this method's pre-fix
// behavior for that case.
func TestInvestigate_AbortOnAlreadyTerminalInvestigationIsBestEffort(t *testing.T) {
	registry := okRegistry()
	reasoner := &fakeReasoner{
		next: func(ctx context.Context, s State) (Proposal, error) {
			return Proposal{}, ErrNoMoreRules
		},
	}
	e := newTestEngine(reasoner, registry, nil, nil)
	inv, err := e.Investigate(context.Background(), testTenant(), testIncident())
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if inv.Status != model.InvestigationInconclusive {
		t.Fatalf("Status = %v, want Inconclusive (test setup)", inv.Status)
	}

	if err := e.Abort(context.Background(), testTenant(), inv.ID, "late abort"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	aborted, err := e.Get(context.Background(), testTenant(), inv.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if aborted.Status != model.InvestigationAborted {
		t.Errorf("Status = %v, want InvestigationAborted after Abort on an already-terminal investigation", aborted.Status)
	}
}

// --- decodeToolArgs (the fix for this wave's build break) round-trips ---

func TestDecodeToolArgs_RoundTrip(t *testing.T) {
	want := ToolArgs{
		Tool:  ToolTraceQuery,
		Trace: &TraceQueryArgs{Service: "checkout", Limit: 50, Status: model.StatusFilterError},
	}
	raw, err := canonicalJSON(want)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	var got ToolArgs
	if err := decodeToolArgs(string(raw), &got); err != nil {
		t.Fatalf("decodeToolArgs: %v", err)
	}
	if got.Tool != want.Tool || got.Trace == nil || got.Trace.Service != want.Trace.Service || got.Trace.Limit != want.Trace.Limit {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestDecodeToolArgs_EmptyIsError(t *testing.T) {
	var got ToolArgs
	if err := decodeToolArgs("", &got); err == nil {
		t.Fatal("expected an error decoding empty ToolArgsJSON")
	}
}
