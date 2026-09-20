package sampler

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// drainRED reads every currently-buffered sample off mgr.REDSamples()
// without blocking.
func drainRED(mgr *Manager) map[string]model.REDSample {
	out := map[string]model.REDSample{}
	for {
		select {
		case s := <-mgr.REDSamples():
			out[s.Service+"/"+s.Operation] = s
		default:
			return out
		}
	}
}

// TestConsume_ExtractsREDForEverySpanRegardlessOfKeepDecision covers
// FR-F02-5/DR-9's "RED before every discard": RED accumulation happens
// inside Consume, before any shard/assembly/keep decision, so it must be
// present for both a trace that will end up kept (error) and one that will
// end up dropped by a saturated max_keep_rate cap.
func TestConsume_ExtractsREDForEverySpanRegardlessOfKeepDecision(t *testing.T) {
	clock := &fakeClock{now: time.Unix(2000, 0)}
	wal := NewMemWAL()
	cfg := DefaultPolicyConfig()
	cfg.MaxKeepRate = 0.0 // force every non-error decision to be shed
	cfg.RarePathKeepsPerMin = 0
	cfg.FloorTracesPerMinPerService = 0
	cfg.HealthySampleRate = 0
	policy := NewDefaultPolicy(cfg, clock)
	preds := NewPredicateSet()
	mgr := NewManager(DefaultAssemblyConfig(), clock, wal, policy, policy, preds, 1)

	tid := model.TenantID("t1")
	spansErr := []model.Span{mkSpan(mkTraceID(1), mkSpanID(1), model.SpanID{}, "svc-a", "op-a", 0, uint64(5*time.Millisecond), true)}
	spansOk := []model.Span{mkSpan(mkTraceID(2), mkSpanID(1), model.SpanID{}, "svc-b", "op-b", 0, uint64(5*time.Millisecond), false)}

	if err := mgr.Consume(context.Background(), tid, spansErr); err != nil {
		t.Fatalf("Consume (error trace): %v", err)
	}
	if err := mgr.Consume(context.Background(), tid, spansOk); err != nil {
		t.Fatalf("Consume (healthy trace): %v", err)
	}

	// RED must already be extracted at this point — before either trace has
	// been finalized/decided at all.
	mgr.DrainRED()
	got := drainRED(mgr)

	sampleA, ok := got["svc-a/op-a"]
	if !ok {
		t.Fatalf("RED sample missing for svc-a/op-a (the trace that will be kept): %+v", got)
	}
	if sampleA.Calls != 1 || sampleA.Errors != 1 {
		t.Fatalf("svc-a/op-a RED sample wrong: %+v", sampleA)
	}

	sampleB, ok := got["svc-b/op-b"]
	if !ok {
		t.Fatalf("RED sample missing for svc-b/op-b (the trace that will be dropped/shed): %+v", got)
	}
	if sampleB.Calls != 1 || sampleB.Errors != 0 {
		t.Fatalf("svc-b/op-b RED sample wrong: %+v", sampleB)
	}

	// Now finalize both traces past hard_timeout and confirm the healthy one
	// is in fact NOT kept (proving its RED capture above was independent of
	// the eventual drop decision, not a side effect of being kept).
	clock.now = clock.now.Add(time.Hour)
	mgr.Tick(context.Background())

	decisions := map[model.TraceID]Decision{}
	for i := 0; i < 2; i++ {
		select {
		case d := <-mgr.Decisions():
			decisions[d.TraceID] = d
		default:
			t.Fatalf("expected 2 decisions after Tick past hard_timeout, got %d", i)
		}
	}
	dErr, ok := decisions[mkTraceID(1)]
	if !ok || !dErr.Keep || dErr.Reason != model.KeepError {
		t.Fatalf("expected the error trace to be kept: %+v", dErr)
	}
	dOk, ok := decisions[mkTraceID(2)]
	if !ok || dOk.Keep {
		t.Fatalf("expected the healthy trace to be dropped under the saturated cap: %+v", dOk)
	}
}

// TestConsume_REDAccumulatesAcrossMultipleSpansAndBatches covers RED
// accuracy for multi-span, multi-batch traces within the same 10s bucket:
// Calls/Errors/DurationSumNanos must sum across every span seen, not just
// the last Consume call.
func TestConsume_REDAccumulatesAcrossMultipleSpansAndBatches(t *testing.T) {
	clock := &fakeClock{now: time.Unix(3000, 0)}
	wal := NewMemWAL()
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	preds := NewPredicateSet()
	mgr := NewManager(DefaultAssemblyConfig(), clock, wal, policy, policy, preds, 1)
	tid := model.TenantID("t1")

	batch1 := []model.Span{
		mkSpan(mkTraceID(10), mkSpanID(1), model.SpanID{}, "svc-c", "op-c", 0, uint64(2*time.Millisecond), false),
		mkSpan(mkTraceID(11), mkSpanID(1), model.SpanID{}, "svc-c", "op-c", 0, uint64(3*time.Millisecond), true),
	}
	batch2 := []model.Span{
		mkSpan(mkTraceID(12), mkSpanID(1), model.SpanID{}, "svc-c", "op-c", 0, uint64(4*time.Millisecond), false),
	}
	if err := mgr.Consume(context.Background(), tid, batch1); err != nil {
		t.Fatalf("Consume batch1: %v", err)
	}
	if err := mgr.Consume(context.Background(), tid, batch2); err != nil {
		t.Fatalf("Consume batch2: %v", err)
	}

	mgr.DrainRED()
	got := drainRED(mgr)
	sample, ok := got["svc-c/op-c"]
	if !ok {
		t.Fatalf("RED sample missing for svc-c/op-c: %+v", got)
	}
	if sample.Calls != 3 {
		t.Fatalf("Calls = %d, want 3 (summed across both batches)", sample.Calls)
	}
	if sample.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", sample.Errors)
	}
	wantDur := uint64(2*time.Millisecond) + uint64(3*time.Millisecond) + uint64(4*time.Millisecond)
	if sample.DurationSumNanos != wantDur {
		t.Fatalf("DurationSumNanos = %d, want %d", sample.DurationSumNanos, wantDur)
	}
}
