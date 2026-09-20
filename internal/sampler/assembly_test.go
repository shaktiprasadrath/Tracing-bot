package sampler

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

func newTestManager(clock *fakeClock, acfg AssemblyConfig) *Manager {
	wal := NewMemWAL()
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	preds := NewPredicateSet()
	return NewManager(acfg, clock, wal, policy, policy, preds, 1)
}

// TestManager_Tick_FinalizesOnIdleTimeout covers AC-F02-2's first half: a
// trace with no further spans finalizes once idle_timeout has elapsed since
// its last-seen span, not before.
func TestManager_Tick_FinalizesOnIdleTimeout(t *testing.T) {
	clock := &fakeClock{now: time.Unix(3000, 0)}
	acfg := AssemblyConfig{IdleTimeout: 8 * time.Second, HardTimeout: 30 * time.Second, WheelTick: 250 * time.Millisecond}
	mgr := newTestManager(clock, acfg)

	tid := model.TenantID("t1")
	spans := []model.Span{mkSpan(mkTraceID(1), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)}
	if err := mgr.Consume(context.Background(), tid, spans); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	// 7s later (< 8s idle_timeout): still open.
	clock.now = clock.now.Add(7 * time.Second)
	mgr.Tick(context.Background())
	if st := mgr.Stats(); st.OpenTraces != 1 {
		t.Fatalf("trace finalized before idle_timeout elapsed: OpenTraces=%d, want 1", st.OpenTraces)
	}

	// 9s total since the last span (> 8s idle_timeout): finalizes.
	clock.now = clock.now.Add(2 * time.Second)
	mgr.Tick(context.Background())
	if st := mgr.Stats(); st.OpenTraces != 0 {
		t.Fatalf("trace not finalized after idle_timeout elapsed: OpenTraces=%d, want 0", st.OpenTraces)
	}

	select {
	case d := <-mgr.Decisions():
		if d.TraceID != mkTraceID(1) {
			t.Fatalf("unexpected decision trace id: %+v", d)
		}
	default:
		t.Fatalf("expected a Decision after idle-timeout finalize")
	}
}

// TestManager_Tick_FinalizesOnHardTimeoutDespiteContinuousActivity covers
// AC-F02-2's second half: a trace that keeps receiving spans well inside
// idle_timeout (so it never goes idle) must still finalize once
// hard_timeout has elapsed since it was first seen.
func TestManager_Tick_FinalizesOnHardTimeoutDespiteContinuousActivity(t *testing.T) {
	clock := &fakeClock{now: time.Unix(4000, 0)}
	acfg := AssemblyConfig{IdleTimeout: 8 * time.Second, HardTimeout: 30 * time.Second, WheelTick: 250 * time.Millisecond}
	mgr := newTestManager(clock, acfg)

	tid := model.TenantID("t1")
	id := mkTraceID(2)

	// A span every 5s (well under the 8s idle_timeout) for 7 rounds (30s
	// total elapsed by the last Tick): hard_timeout must fire despite the
	// trace never going idle.
	for i := 0; i < 7; i++ {
		spans := []model.Span{mkSpan(id, mkSpanID(uint64(i+1)), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)}
		if err := mgr.Consume(context.Background(), tid, spans); err != nil {
			t.Fatalf("Consume round %d: %v", i, err)
		}
		mgr.Tick(context.Background())
		clock.now = clock.now.Add(5 * time.Second)
	}

	if st := mgr.Stats(); st.OpenTraces != 0 {
		t.Fatalf("trace not finalized by hard_timeout despite continuous activity: OpenTraces=%d, want 0", st.OpenTraces)
	}
	select {
	case d := <-mgr.Decisions():
		if d.TraceID != id {
			t.Fatalf("unexpected decision trace id: %+v", d)
		}
	default:
		t.Fatalf("expected a Decision once hard_timeout was exceeded")
	}
}

// TestManager_Tick_DoesNotFinalizeFreshTraces is the negative control for
// both timeout tests above: a trace well inside both idle_timeout and
// hard_timeout must never be touched by Tick.
func TestManager_Tick_DoesNotFinalizeFreshTraces(t *testing.T) {
	clock := &fakeClock{now: time.Unix(5000, 0)}
	acfg := DefaultAssemblyConfig()
	mgr := newTestManager(clock, acfg)

	tid := model.TenantID("t1")
	spans := []model.Span{mkSpan(mkTraceID(3), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)}
	if err := mgr.Consume(context.Background(), tid, spans); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	clock.now = clock.now.Add(1 * time.Second)
	mgr.Tick(context.Background())
	if st := mgr.Stats(); st.OpenTraces != 1 {
		t.Fatalf("fresh trace was finalized prematurely: OpenTraces=%d, want 1", st.OpenTraces)
	}
	select {
	case d := <-mgr.Decisions():
		t.Fatalf("unexpected decision emitted for a still-open trace: %+v", d)
	default:
	}
}

// TestManager_FlushAll_ForceCompletesEveryOpenTraceAsKeepShed covers
// FR-F02-9: graceful shutdown must lose 0 in-flight traces by force-keeping
// (Reason=KeepShed) every still-open trace, regardless of timeouts.
func TestManager_FlushAll_ForceCompletesEveryOpenTraceAsKeepShed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(6000, 0)}
	mgr := newTestManager(clock, DefaultAssemblyConfig())
	tid := model.TenantID("t1")

	for i := uint64(1); i <= 3; i++ {
		spans := []model.Span{mkSpan(mkTraceID(i), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)}
		if err := mgr.Consume(context.Background(), tid, spans); err != nil {
			t.Fatalf("Consume: %v", err)
		}
	}
	if st := mgr.Stats(); st.OpenTraces != 3 {
		t.Fatalf("OpenTraces = %d before FlushAll, want 3", st.OpenTraces)
	}

	if err := mgr.FlushAll(context.Background()); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	if st := mgr.Stats(); st.OpenTraces != 0 {
		t.Fatalf("OpenTraces = %d after FlushAll, want 0", st.OpenTraces)
	}

	// Per Decision.Keep's documented convention (sampler.go), KeepShed is a
	// Keep=false class like KeepDropped — "loses 0 in-flight traces" means a
	// Decision (and prior RED) is recorded for every open trace so none are
	// silently dropped uncounted, not that shutdown persists trace bodies.
	seen := map[model.TraceID]bool{}
	for i := 0; i < 3; i++ {
		select {
		case d := <-mgr.Decisions():
			if d.Keep || d.Reason != model.KeepShed {
				t.Fatalf("FlushAll decision not Keep=false/KeepShed: %+v", d)
			}
			seen[d.TraceID] = true
		default:
			t.Fatalf("expected 3 decisions from FlushAll, got %d", i)
		}
	}
	for i := uint64(1); i <= 3; i++ {
		if !seen[mkTraceID(i)] {
			t.Fatalf("FlushAll lost trace %d", i)
		}
	}
}
