package anomaly

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/topology"
)

// fakeBaselines is a minimal BaselineReader stub keyed by service|operation.
type fakeBaselines struct {
	m map[string]Baseline
}

func newFakeBaselines() *fakeBaselines { return &fakeBaselines{m: make(map[string]Baseline)} }

func (f *fakeBaselines) set(service, operation string, b Baseline) {
	f.m[service+"|"+operation] = b
}

func (f *fakeBaselines) Get(tid model.TenantID, service, operation string, at time.Time) (Baseline, bool) {
	b, ok := f.m[service+"|"+operation]
	return b, ok
}

func warmBaseline(p95 uint64, errorEWMA, rpsEWMA float64) Baseline {
	return Baseline{
		Warmed: true, Provisional: false,
		Q:         model.Quantiles{P50Nanos: p95 / 2, P95Nanos: p95, P99Nanos: p95 * 2},
		ErrorEWMA: errorEWMA, RPSEWMA: rpsEWMA,
	}
}

// --- latency_shift (FR-F05-5, AC-F05-2) ---

func TestLatencyShift_FiresOnRealShift(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baseP95 := uint64(100 * time.Millisecond)
	baselines.set("checkout", "POST", warmBaseline(baseP95, 0.01, 10))

	det := NewLatencyShiftDetector(cfg)
	now := time.Now()
	// obs.P95 = 200ms: ratio 2.0 >= 1.5, delta 100ms >= 50ms, calls 50>=20.
	obsP95 := uint64(200 * time.Millisecond)
	in := EvalInput{
		Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)},
		Baselines: baselines,
		Samples:   []model.REDSample{sampleAt("checkout", "POST", now, 50, 0, obsP95/2, obsP95, obsP95*2)},
	}
	ctx := context.Background()
	// Tick 1: arms consecutive-breach counter, no event yet (debounce_ticks=2).
	ev1, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev1) != 0 {
		t.Fatalf("expected no event on first breaching tick (debounce), got %d", len(ev1))
	}
	// Tick 2: confirms.
	ev2, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev2) != 1 {
		t.Fatalf("expected 1 event on confirming tick, got %d", len(ev2))
	}
	e := ev2[0]
	if e.Kind != model.AnomalyLatencyShift {
		t.Errorf("Kind = %v, want AnomalyLatencyShift", e.Kind)
	}
	wantScore := clamp01(0.5*min1((2.0-1)/1.0) + 0.5*min1(float64(obsP95-baseP95)/(4*float64(50*time.Millisecond))))
	if e.Score != wantScore {
		t.Errorf("Score = %v, want %v (DR-14 §14.4 formula)", e.Score, wantScore)
	}
}

func TestLatencyShift_NoFalsePositiveOnNormalNoise(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baseP95 := uint64(100 * time.Millisecond)
	baselines.set("checkout", "POST", warmBaseline(baseP95, 0.01, 10))

	det := NewLatencyShiftDetector(cfg)
	now := time.Now()
	// obs.P95 = 110ms: ratio 1.1 < 1.5 threshold -- normal noise.
	obsP95 := uint64(110 * time.Millisecond)
	in := EvalInput{
		Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)},
		Baselines: baselines,
		Samples:   []model.REDSample{sampleAt("checkout", "POST", now, 50, 0, obsP95/2, obsP95, obsP95*2)},
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		ev, err := det.Evaluate(ctx, "t1", in)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(ev) != 0 {
			t.Fatalf("tick %d: expected 0 false-positive events on normal noise, got %d", i, len(ev))
		}
	}
}

func TestLatencyShift_BoundaryJustBelowAndAtThreshold(t *testing.T) {
	cfg := testConfig()
	baseP95 := uint64(100 * time.Millisecond) // 100,000,000 ns
	ctx := context.Background()

	// Just below ratio threshold (1.5x - epsilon, but still above abs delta):
	// should never breach even across many ticks.
	t.Run("just_below_ratio", func(t *testing.T) {
		baselines := newFakeBaselines()
		baselines.set("svc", "op", warmBaseline(baseP95, 0.01, 10))
		det := NewLatencyShiftDetector(cfg)
		obsP95 := uint64(float64(baseP95) * 1.49) // ratio 1.49 < 1.5
		now := time.Now()
		in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
			Samples: []model.REDSample{sampleAt("svc", "op", now, 50, 0, obsP95/2, obsP95, obsP95*2)}}
		for i := 0; i < 3; i++ {
			ev, _ := det.Evaluate(ctx, "t1", in)
			if len(ev) != 0 {
				t.Fatalf("tick %d: expected no event just below ratio threshold, got %d", i, len(ev))
			}
		}
	})

	// Exactly at ratio and delta threshold: obs.P95 = base*1.5, and
	// base*1.5-base = base*0.5 = 50ms exactly when base=100ms. Both are
	// >= comparisons, so this must breach.
	t.Run("exactly_at_threshold", func(t *testing.T) {
		baselines := newFakeBaselines()
		baselines.set("svc", "op", warmBaseline(baseP95, 0.01, 10))
		det := NewLatencyShiftDetector(cfg)
		obsP95 := uint64(float64(baseP95) * 1.5) // exactly ratio 1.5, delta exactly 50ms
		now := time.Now()
		in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
			Samples: []model.REDSample{sampleAt("svc", "op", now, 50, 0, obsP95/2, obsP95, obsP95*2)}}
		det.Evaluate(ctx, "t1", in)
		ev, _ := det.Evaluate(ctx, "t1", in)
		if len(ev) != 1 {
			t.Fatalf("expected event exactly at threshold (>= is inclusive), got %d", len(ev))
		}
	})
}

// TestLatencyShift_SubThresholdScoreStillReturned locks in a fix: DR-14
// §14.4 says an event with Score < min_event_score is "recorded in
// anomaly_event but never reaches the Grouper" -- which requires Evaluate to
// still return it (Evaluate is the only place such an event could ever be
// persisted from, since it is documented pure/no-I/O). The original
// implementation silently dropped these events from Evaluate's return
// value, making that persistence impossible; this test fails against that
// version.
func TestLatencyShift_SubThresholdScoreStillReturned(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baseP95 := uint64(100 * time.Millisecond)
	baselines.set("svc", "op", warmBaseline(baseP95, 0.01, 10))
	det := NewLatencyShiftDetector(cfg)
	// Exactly at the ratio+delta trigger boundary: score works out to 0.375,
	// below min_event_score (0.55), but the trigger legitimately fired.
	obsP95 := uint64(float64(baseP95) * 1.5)
	now := time.Now()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, 50, 0, obsP95/2, obsP95, obsP95*2)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 1 {
		t.Fatalf("expected the sub-threshold-score event to still be returned by Evaluate, got %d events", len(ev))
	}
	if ev[0].Score >= cfg.Thresholds.MinEventScore {
		t.Fatalf("test setup error: expected a sub-threshold score, got %v >= %v", ev[0].Score, cfg.Thresholds.MinEventScore)
	}
}

func TestLatencyShift_SuppressedBelowMinCalls(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baseP95 := uint64(100 * time.Millisecond)
	baselines.set("svc", "op", warmBaseline(baseP95, 0.01, 10))
	det := NewLatencyShiftDetector(cfg)
	obsP95 := uint64(300 * time.Millisecond) // huge breach, but calls < min_calls (20)
	now := time.Now()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, 10, 0, obsP95/2, obsP95, obsP95*2)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 0 {
		t.Fatalf("expected suppression below min_calls, got %d events", len(ev))
	}
}

// --- error_burst (FR-F05-6, AC-F05-2) ---

func TestErrorBurst_FiresOnRealBurst(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baselines.set("checkout", "POST", warmBaseline(100*uint64(time.Millisecond), 0.01, 10))
	det := NewErrorBurstDetector(cfg)
	now := time.Now()
	// obsRate=0.4: delta 0.39>=0.05, ratio 40x>=3x, calls>=20.
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("checkout", "POST", now, 100, 40, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	ev1, _ := det.Evaluate(ctx, "t1", in)
	if len(ev1) != 0 {
		t.Fatalf("expected debounce on first tick, got %d events", len(ev1))
	}
	ev2, _ := det.Evaluate(ctx, "t1", in)
	if len(ev2) != 1 {
		t.Fatalf("expected 1 event on confirming tick, got %d", len(ev2))
	}
	if ev2[0].Kind != model.AnomalyErrorBurst {
		t.Errorf("Kind = %v, want AnomalyErrorBurst", ev2[0].Kind)
	}
}

func TestErrorBurst_NoFalsePositiveOnNormalNoise(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baselines.set("checkout", "POST", warmBaseline(100*uint64(time.Millisecond), 0.02, 10))
	det := NewErrorBurstDetector(cfg)
	now := time.Now()
	// obsRate = 0.03: delta 0.01 < 0.05 -- normal noise around a 2% baseline.
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("checkout", "POST", now, 100, 3, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		ev, _ := det.Evaluate(ctx, "t1", in)
		if len(ev) != 0 {
			t.Fatalf("tick %d: expected no false positive on normal noise, got %d", i, len(ev))
		}
	}
}

func TestErrorBurst_BoundaryAtThreshold(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baselineErr := 0.02
	baselines.set("svc", "op", warmBaseline(100*uint64(time.Millisecond), baselineErr, 10))
	det := NewErrorBurstDetector(cfg)
	now := time.Now()
	// obsRate exactly baselineErr + 0.05 = 0.07, and 0.07 >= baselineErr*3=0.06: both hold.
	calls := uint64(1000)
	errs := uint64(0.07 * 1000)
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, calls, errs, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 1 {
		t.Fatalf("expected event exactly at threshold, got %d", len(ev))
	}
}

// --- throughput_drop (FR-F05-13, AC-F05-13) ---

func TestThroughputDrop_FiresExactlyAtRatio(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baselines.set("svc", "op", warmBaseline(100*uint64(time.Millisecond), 0.01, 10)) // RPSEWMA=10
	det := NewThroughputDropDetector(cfg)
	now := time.Now()
	windowSeconds := 90.0
	// obs.RPS <= base.RPSEWMA*0.5 = 5. Set calls so obsRPS == 5 exactly.
	calls := uint64(5 * windowSeconds)
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(time.Duration(windowSeconds) * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, calls, 0, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 1 {
		t.Fatalf("expected fire exactly at obs.RPS == base.RPSEWMA*ratio, got %d events", len(ev))
	}
}

func TestThroughputDrop_NoFireAboveRatio(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	baselines.set("svc", "op", warmBaseline(100*uint64(time.Millisecond), 0.01, 10)) // RPSEWMA=10
	det := NewThroughputDropDetector(cfg)
	now := time.Now()
	windowSeconds := 90.0
	// obsRPS = 6 > 5 (threshold): should not fire.
	calls := uint64(6 * windowSeconds)
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(time.Duration(windowSeconds) * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, calls, 0, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 0 {
		t.Fatalf("expected no fire above ratio threshold, got %d events", len(ev))
	}
}

func TestThroughputDrop_SuppressedBelowMinRPS(t *testing.T) {
	cfg := testConfig()
	baselines := newFakeBaselines()
	// RPSEWMA below min_rps (0.1): must be suppressed even with a "drop".
	baselines.set("svc", "op", warmBaseline(100*uint64(time.Millisecond), 0.01, 0.05))
	det := NewThroughputDropDetector(cfg)
	now := time.Now()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now.Add(90 * time.Second)}, Baselines: baselines,
		Samples: []model.REDSample{sampleAt("svc", "op", now, 0, 0, 1e6, 2e6, 3e6)}}
	ctx := context.Background()
	det.Evaluate(ctx, "t1", in)
	ev, _ := det.Evaluate(ctx, "t1", in)
	if len(ev) != 0 {
		t.Fatalf("expected suppression below min_rps, got %d events", len(ev))
	}
}

// --- new_error_signature (FR-F05-7, AC-F05-2) ---

func TestNewErrorSignature_FiresOncePerNewSignature(t *testing.T) {
	cfg := testConfig()
	det := NewNewErrorSignatureDetector(cfg)
	now := time.Now()
	sig := store.ErrorSignature{Tenant: "t1", ID: "sig-1", Service: "svc", FirstSeen: now.Add(-10 * time.Second), Samples: 5}
	in := EvalInput{Now: now, Window: model.Window{Start: now.Add(-90 * time.Second), End: now}, ErrorSigs: []store.ErrorSignature{sig}}
	ctx := context.Background()

	ev1, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev1) != 1 {
		t.Fatalf("expected 1 event for new signature, got %d", len(ev1))
	}
	if ev1[0].Kind != model.AnomalyNewErrorSignature {
		t.Errorf("Kind = %v, want AnomalyNewErrorSignature", ev1[0].Kind)
	}

	// Repeat tick with the same signature: must not re-fire.
	ev2, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev2) != 0 {
		t.Fatalf("expected 0 events on repeat of an already-emitted signature, got %d", len(ev2))
	}
}

func TestNewErrorSignature_SuppressedBelowMinCalls(t *testing.T) {
	cfg := testConfig()
	det := NewNewErrorSignatureDetector(cfg)
	now := time.Now()
	sig := store.ErrorSignature{Tenant: "t1", ID: "sig-2", Service: "svc", FirstSeen: now.Add(-5 * time.Second), Samples: 4} // < min_calls (5)
	in := EvalInput{Now: now, Window: model.Window{Start: now.Add(-90 * time.Second), End: now}, ErrorSigs: []store.ErrorSignature{sig}}
	ctx := context.Background()
	ev, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev) != 0 {
		t.Fatalf("expected suppression below new_error_sig_min_calls, got %d events", len(ev))
	}
}

func TestNewErrorSignature_OldSignatureNotFlagged(t *testing.T) {
	cfg := testConfig()
	det := NewNewErrorSignatureDetector(cfg)
	now := time.Now()
	// FirstSeen well outside the eval window -- not "newly seen" this tick.
	sig := store.ErrorSignature{Tenant: "t1", ID: "sig-3", Service: "svc", FirstSeen: now.Add(-48 * time.Hour), Samples: 50}
	in := EvalInput{Now: now, Window: model.Window{Start: now.Add(-90 * time.Second), End: now}, ErrorSigs: []store.ErrorSignature{sig}}
	ctx := context.Background()
	ev, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(ev) != 0 {
		t.Fatalf("expected an old, already-known signature not to be flagged, got %d events", len(ev))
	}
}

// --- topology_change (FR-F05-8, AC-F05-2) ---

func TestTopologyChange_NewEdgeFromChannel(t *testing.T) {
	cfg := testConfig()
	ch := make(chan topology.ChangeEvent, 4)
	det := NewTopologyChangeDetector(cfg, ch, nil)
	now := time.Now()
	edge := topology.Edge{ID: "e1", Tenant: "t1", Caller: "a", Callee: "b", Calls: 10}
	ch <- topology.ChangeEvent{Kind: "NewEdge", Edge: edge, Detected: now}
	close(ch)

	ctx := context.Background()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	events, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 NewEdge event, got %d", len(events))
	}
	if events[0].Kind != model.AnomalyTopologyChange {
		t.Errorf("Kind = %v, want AnomalyTopologyChange", events[0].Kind)
	}
	if events[0].Score != 0.50 {
		t.Errorf("Score = %v, want 0.50 (DR-14 §14.4 NewEdge)", events[0].Score)
	}
}

func TestTopologyChange_NewEdgeBelowMinCallsSuppressed(t *testing.T) {
	cfg := testConfig()
	ch := make(chan topology.ChangeEvent, 4)
	det := NewTopologyChangeDetector(cfg, ch, nil)
	now := time.Now()
	edge := topology.Edge{ID: "e2", Tenant: "t1", Caller: "a", Callee: "b", Calls: 2} // < new_edge_min_calls (5)
	ch <- topology.ChangeEvent{Kind: "NewEdge", Edge: edge, Detected: now}
	close(ch)
	ctx := context.Background()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	events, _ := det.Evaluate(ctx, "t1", in)
	if len(events) != 0 {
		t.Fatalf("expected suppression below new_edge_min_calls, got %d", len(events))
	}
}

func TestTopologyChange_VanishedEdgeScoring(t *testing.T) {
	cfg := testConfig()
	ch := make(chan topology.ChangeEvent, 4)
	det := NewTopologyChangeDetector(cfg, ch, nil)
	now := time.Now()
	edge := topology.Edge{ID: "e3", Tenant: "t1", Caller: "a", Callee: "b", Calls: 1500} // >= 1000 in prior 24h
	ch <- topology.ChangeEvent{Kind: "VanishedEdge", Edge: edge, Detected: now}
	close(ch)
	ctx := context.Background()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	events, _ := det.Evaluate(ctx, "t1", in)
	if len(events) != 1 {
		t.Fatalf("expected 1 VanishedEdge event, got %d", len(events))
	}
	if events[0].Score != 0.60 {
		t.Errorf("Score = %v, want 0.60 (DR-14 §14.4 VanishedEdge)", events[0].Score)
	}
}

// fakeEdgeMetaReader satisfies EdgeMetaReader for the durable-reconciliation
// half of the topology_change detector.
type fakeEdgeMetaReader struct{ edges []topology.Edge }

func (f *fakeEdgeMetaReader) NewEdgesSince(ctx context.Context, tid model.TenantID, since time.Time) ([]topology.Edge, error) {
	return f.edges, nil
}

func TestTopologyChange_DurableReconciliationEmitsEvent(t *testing.T) {
	cfg := testConfig()
	edge := topology.Edge{ID: "e4", Tenant: "t1", Caller: "a", Callee: "c", Calls: 10}
	reader := &fakeEdgeMetaReader{edges: []topology.Edge{edge}}
	det := NewTopologyChangeDetector(cfg, nil, reader)
	now := time.Now()
	ctx := context.Background()
	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	events, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event from durable reconciliation path (channel is nil), got %d", len(events))
	}
	if events[0].Kind != model.AnomalyTopologyChange {
		t.Errorf("Kind = %v, want AnomalyTopologyChange", events[0].Kind)
	}

	// Second tick: the same edge should not be re-emitted (dedupe by
	// Edge.ID|Kind).
	events2, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(events2) != 0 {
		t.Fatalf("expected dedupe on repeat reconciliation read, got %d events", len(events2))
	}
}

// TestTopologyChange_DedupeIsPerTenant locks in a w12 review fix: the
// detector's local emitted-dedupe used to be keyed by Edge.ID+Kind alone,
// with no tenant component. Since edgeIDFor hashes only caller/callee/
// protocol (not tenant), two different tenants with an identically-shaped
// edge (e.g. both have "frontend"->"backend" over http) would collide, and
// whichever tenant's NewEdge got processed first would permanently
// suppress the other tenant's genuinely-new-to-them edge. A single
// TopologyChangeDetector instance must be safe to reuse across tenants,
// matching every other detector in this package (all of which key their
// dedupe/consecutive maps by tid).
func TestTopologyChange_DedupeIsPerTenant(t *testing.T) {
	cfg := testConfig()
	ch := make(chan topology.ChangeEvent, 8)
	det := NewTopologyChangeDetector(cfg, ch, nil)
	now := time.Now()

	// Same Edge.ID, same Kind, two different tenants.
	edgeT1 := topology.Edge{ID: "shared-edge", Tenant: "t1", Caller: "frontend", Callee: "backend", Calls: 10}
	edgeT2 := topology.Edge{ID: "shared-edge", Tenant: "t2", Caller: "frontend", Callee: "backend", Calls: 10}
	ch <- topology.ChangeEvent{Kind: "NewEdge", Edge: edgeT1, Detected: now}
	ctx := context.Background()
	in1 := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	ev1, err := det.Evaluate(ctx, "t1", in1)
	if err != nil {
		t.Fatalf("Evaluate (t1): %v", err)
	}
	if len(ev1) != 1 {
		t.Fatalf("expected 1 event for t1's edge, got %d", len(ev1))
	}

	ch <- topology.ChangeEvent{Kind: "NewEdge", Edge: edgeT2, Detected: now}
	ev2, err := det.Evaluate(ctx, "t2", in1)
	if err != nil {
		t.Fatalf("Evaluate (t2): %v", err)
	}
	if len(ev2) != 1 {
		t.Fatalf("expected t2's identically-shaped edge to still fire (tenant-scoped dedupe), got %d events", len(ev2))
	}
}

// TestTopologyChange_DedupeExpiresAfterTTL locks in a w12 review fix: the
// detector's local emitted-dedupe used to be permanent (life of the
// process), which would silently and permanently swallow a legitimate
// second NewEdge occurrence for the same edge (e.g. after it genuinely
// vanished and reappeared weeks later -- topology.LiveGraph's own
// newEdgeWindow logic, fixed in the w10 review, correctly refires
// ChangeEvent{NewEdge} in that case). Dedupe must be bounded by
// grouping.dedupe_ttl, not forever.
func TestTopologyChange_DedupeExpiresAfterTTL(t *testing.T) {
	cfg := testConfig()
	edge := topology.Edge{ID: "e-reappear", Tenant: "t1", Caller: "a", Callee: "b", Calls: 10}
	reader := &fakeEdgeMetaReader{edges: []topology.Edge{edge}}
	det := NewTopologyChangeDetector(cfg, nil, reader)
	ctx := context.Background()
	now := time.Now()

	in := EvalInput{Now: now, Window: model.Window{Start: now, End: now}}
	ev1, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate (first): %v", err)
	}
	if len(ev1) != 1 {
		t.Fatalf("expected 1 event on first sighting, got %d", len(ev1))
	}

	// Well within dedupe_ttl: must still be suppressed.
	within := EvalInput{Now: now.Add(cfg.Grouping.DedupeTTL / 2), Window: model.Window{Start: now, End: now}}
	ev2, err := det.Evaluate(ctx, "t1", within)
	if err != nil {
		t.Fatalf("Evaluate (within TTL): %v", err)
	}
	if len(ev2) != 0 {
		t.Fatalf("expected dedupe within dedupe_ttl, got %d events", len(ev2))
	}

	// Past dedupe_ttl since the first emission: the same edge must be
	// eligible to fire again (a genuine re-occurrence).
	after := EvalInput{Now: now.Add(cfg.Grouping.DedupeTTL + time.Minute), Window: model.Window{Start: now, End: now}}
	ev3, err := det.Evaluate(ctx, "t1", after)
	if err != nil {
		t.Fatalf("Evaluate (after TTL): %v", err)
	}
	if len(ev3) != 1 {
		t.Fatalf("expected re-emission after dedupe_ttl has elapsed, got %d events", len(ev3))
	}
}

// --- deploy enrichment (FR-F05-9, AC-F05-2) ---

func TestDeployEnrichment_AttachesToInterectingEvent(t *testing.T) {
	cfg := testConfig()
	idx := NewDeployIndex(cfg)
	ctx := context.Background()
	deployAt := time.Now()
	marker := model.DeployMarker{ID: "dep-1", Tenant: "t1", Service: "checkout", At: deployAt}
	if err := idx.Record(ctx, "t1", marker); err != nil {
		t.Fatalf("Record: %v", err)
	}

	baselines := newFakeBaselines()
	baselines.set("checkout", "POST", warmBaseline(100*uint64(time.Millisecond), 0.01, 10))
	det := NewLatencyShiftDetector(cfg)
	// Event window starts 5 minutes after the deploy -- inside the 30m
	// correlation window.
	evAt := deployAt.Add(5 * time.Minute)
	obsP95 := uint64(300 * time.Millisecond)
	in := EvalInput{
		Now: evAt, Window: model.Window{Start: evAt, End: evAt.Add(90 * time.Second)},
		Baselines: baselines, Deploys: idx,
		Samples: []model.REDSample{sampleAt("checkout", "POST", evAt, 50, 0, obsP95/2, obsP95, obsP95*2)},
	}
	det.Evaluate(ctx, "t1", in)
	events, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if len(e.DeployMarkerIDs) != 1 || e.DeployMarkerIDs[0] != "dep-1" {
		t.Fatalf("expected DeployMarkerIDs=[dep-1], got %v", e.DeployMarkerIDs)
	}
	baseScore := clamp01(0.5*min1((3.0-1)/1.0) + 0.5*min1(float64(obsP95-100*uint64(time.Millisecond))/(4*float64(50*time.Millisecond))))
	wantScore := clamp01(baseScore + 0.10)
	if e.Score != wantScore {
		t.Errorf("Score = %v, want %v (base score + 0.10 deploy enrichment, clamped)", e.Score, wantScore)
	}
}

func TestDeployIndex_PrePostSplit(t *testing.T) {
	cfg := testConfig()
	idx := NewDeployIndex(cfg)
	ctx := context.Background()
	at := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	marker := model.DeployMarker{ID: "dep-2", Tenant: "t1", Service: "svc", At: at}
	pre, post, err := idx.PrePostSplit(ctx, "t1", marker)
	if err != nil {
		t.Fatalf("PrePostSplit: %v", err)
	}
	wantPre := model.Window{Start: at.Add(-30 * time.Minute), End: at}
	wantPost := model.Window{Start: at.Add(2 * time.Minute), End: at.Add(2*time.Minute + 30*time.Minute)}
	if pre != wantPre {
		t.Errorf("pre = %+v, want %+v", pre, wantPre)
	}
	if post != wantPost {
		t.Errorf("post = %+v, want %+v", post, wantPost)
	}
}
