package main

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/sampler"
)

// blockingReasoner is a test-local rca.Reasoner whose NextStep blocks on a
// test-controlled gate, the same pattern internal/rca/engine_test.go's
// TestInvestigate_AbortInterruptsRunningLoop uses: it lets this test observe
// state while an investigation is genuinely in flight (after the FR-F06-12a
// scope predicate has been pushed at Contextualize, but before the
// investigation concludes and the predicate is torn down), rather than
// racing Investigate's synchronous return.
type blockingReasoner struct {
	started chan struct{}
	proceed chan struct{}
}

func (r *blockingReasoner) Kind() model.ReasonerKind { return model.ReasonerRules }

func (r *blockingReasoner) NextStep(ctx context.Context, tid model.TenantID, s rca.State) (rca.Proposal, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.proceed
	return rca.Proposal{Phase: model.PhaseValidate, Done: true}, nil
}

func (r *blockingReasoner) Conclude(ctx context.Context, tid model.TenantID, s rca.State) (model.Conclusion, error) {
	// Deliberately low confidence: FR-F06-12b's phase-B recurrence predicate
	// must NOT be pushed, so the only interest predicate in play throughout
	// this test is the phase-A scope predicate this test is proving actually
	// reaches the running sampler.
	return model.Conclusion{RootCause: "n/a", Confidence: 0}, nil
}

func mkTestSpan(traceN, spanN uint64, service string, startNanos uint64) model.Span {
	var traceID model.TraceID
	var spanID model.SpanID
	traceID[15] = byte(traceN)
	spanID[7] = byte(spanN)
	return model.Span{
		TraceID:       traceID,
		SpanID:        spanID,
		Name:          "GET /widgets",
		StartUnixNano: startNanos,
		EndUnixNano:   startNanos + uint64(time.Millisecond),
		Status:        model.Status{Code: model.StatusOk},
		Resource:      &model.Resource{ServiceName: service},
	}
}

// TestInterestSinkAdapter_RCAPredicateReachesRunningSampler is the proof
// docs/signoffs/architecture-final-signoff.md asked for: that D-X5's
// agent-to-sampler feedback loop is REAL in the running binary, not just
// "the adapter compiles". It assembles the same two concrete types
// cmd/traceiq wires together in production -- a real sampler.Impl and a real
// rca.Engine -- connects them through samplerInterestSink (interest_adapter.go,
// exactly as system.go now does), and drives a real rca.Engine.Investigate
// call for an incident scoped to "svc-interest".
//
// Every OTHER keep path in the sampler's policy is zeroed out, so the only
// possible explanation for a kept trace during the investigation is the
// interest predicate rca.Engine pushed through the adapter at Contextualize
// (FR-F06-12a) -- and the only possible explanation for that SAME trace
// shape being dropped once the investigation has concluded is that the
// adapter's RemoveInterestPredicate call also reached the real sampler.
func TestInterestSinkAdapter_RCAPredicateReachesRunningSampler(t *testing.T) {
	clock := newRealClock()

	acfg := sampler.DefaultAssemblyConfig()
	acfg.IdleTimeout = 20 * time.Millisecond
	acfg.HardTimeout = 200 * time.Millisecond

	pcfg := sampler.DefaultPolicyConfig()
	// Zero every other keep class so KeepInterest is the only path that can
	// explain a kept trace for svc-interest below.
	pcfg.HealthySampleRate = 0
	pcfg.FloorTracesPerMinPerService = 0
	pcfg.RarePathKeepsPerMin = 0
	pcfg.SlowMinDuration = time.Hour

	samp := sampler.NewImpl(acfg, pcfg, clock, sampler.NewMemWAL(), 1)

	registry := rca.NewRegistry(noopTraceStore{}, noopLogStore{}, noopMetricStore{}, noopTopologyStore{}, noopMemoryStore{})
	reasoner := &blockingReasoner{started: make(chan struct{}, 1), proceed: make(chan struct{})}
	// The exact production wiring: newSamplerInterestSink(samp) is what
	// system.go now passes to rca.NewEngine instead of nil.
	eng := rca.NewEngine(rca.NewMemJournal(), registry, reasoner, rca.NewMemObjectStore(), newSamplerInterestSink(samp), clock)

	const tenant = model.TenantID("t-wiring")
	incident := model.Incident{ID: "inc-wiring", EpicenterService: "svc-interest", Fingerprint: "fp-wiring"}

	done := make(chan model.Investigation, 1)
	go func() {
		inv, err := eng.Investigate(context.Background(), tenant, incident)
		if err != nil {
			t.Errorf("Investigate: %v", err)
		}
		done <- inv
	}()

	select {
	case <-reasoner.started:
		// Contextualize (and its SetInterestPredicate call) happens strictly
		// before the first NextStep call, so by the time NextStep has been
		// entered the predicate is guaranteed to have already been pushed.
	case <-time.After(5 * time.Second):
		t.Fatal("investigation never reached its first reasoner step")
	}

	// Drive a trace for the epicenter service through the REAL sampler while
	// the investigation is still open.
	span := mkTestSpan(1, 1, "svc-interest", uint64(time.Now().UnixNano()))
	if err := samp.Consume(context.Background(), tenant, []model.Span{span}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	time.Sleep(40 * time.Millisecond) // past IdleTimeout
	samp.Tick(context.Background())

	select {
	case dec := <-samp.Decisions():
		if !dec.Keep || dec.Reason != model.KeepInterest {
			t.Fatalf("decision while investigation was open = %+v, want Keep=true Reason=KeepInterest (the RCA-pushed predicate never reached the sampler)", dec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no decision produced for the interest-matched trace")
	}

	// Release the reasoner; Investigate concludes (low confidence, per
	// blockingReasoner.Conclude above) and, per engine.go, removes the scope
	// predicate on any terminal status.
	close(reasoner.proceed)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Investigate did not return after the reasoner gate was released")
	}

	// Same service, same everything, AFTER the investigation has concluded:
	// with every other keep path still zeroed, this must now be dropped --
	// proving RemoveInterestPredicate's delegation through the adapter is
	// real too, not just Set's.
	span2 := mkTestSpan(2, 2, "svc-interest", uint64(time.Now().UnixNano()))
	if err := samp.Consume(context.Background(), tenant, []model.Span{span2}); err != nil {
		t.Fatalf("Consume (post-investigation): %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	samp.Tick(context.Background())

	select {
	case dec := <-samp.Decisions():
		if dec.Reason == model.KeepInterest {
			t.Fatalf("decision after investigation concluded was still KeepInterest = %+v; RemoveInterestPredicate did not reach the sampler", dec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no decision produced for the post-investigation trace")
	}
}
