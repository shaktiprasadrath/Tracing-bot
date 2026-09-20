package anomaly

import (
	"context"
	"math"
	"testing"
	"time"

	"traceiq/internal/model"
)

func testConfig() Config {
	return DefaultConfig()
}

func sampleAt(service, op string, at time.Time, calls, errs uint64, p50, p95, p99 uint64) model.REDSample {
	return model.REDSample{
		Tenant:      "t1",
		Service:     service,
		Operation:   op,
		BucketStart: at,
		Resolution:  model.Res10s,
		Calls:       calls,
		Errors:      errs,
		Q:           model.Quantiles{P50Nanos: p50, P95Nanos: p95, P99Nanos: p99},
	}
}

// TestBaseline_ColdState: fewer than warmup_samples global samples -> Cold,
// all detectors suppressed for the key (DR-14 §14.3, FR-F05-4).
func TestBaseline_ColdState(t *testing.T) {
	cfg := testConfig()
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC) // Monday 10:00 UTC

	for i := 0; i < 50; i++ { // well below warmup_samples (200)
		s := sampleAt("svc", "op", base, 100, 1, 1e6, 2e6, 3e6)
		if err := store.Observe(ctx, "t1", s); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	b, ok := store.Reader().Get("t1", "svc", "op", base)
	if !ok {
		t.Fatal("expected baseline entry to exist")
	}
	if b.Warmed || b.Provisional {
		t.Fatalf("expected Cold (Warmed=false, Provisional=false), got Warmed=%v Provisional=%v", b.Warmed, b.Provisional)
	}

	// A detector must suppress: verify via LatencyShiftDetector directly.
	det := NewLatencyShiftDetector(cfg)
	in := EvalInput{
		Now:       base,
		Window:    model.Window{Start: base, End: base.Add(90 * time.Second)},
		Baselines: store.Reader(),
		Samples:   []model.REDSample{sampleAt("svc", "op", base, 100, 1, 1e6, 10e6, 10e6)}, // huge breach if evaluated
	}
	events, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("Cold state must suppress detectors, got %d events", len(events))
	}
}

// TestBaseline_GlobalOnlyState: global warmed but the specific season slot
// has fewer than warmup_samples_per_bucket samples -> GlobalOnly,
// Provisional=true, thresholds widened by cold_start_multiplier.
func TestBaseline_GlobalOnlyState(t *testing.T) {
	cfg := testConfig()
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

	// Spread 220 samples across 24 distinct hour-of-day buckets so the
	// global slot warms (>=200) while any single hour bucket stays under
	// warmup_samples_per_bucket (30): ~9 samples/hour.
	for i := 0; i < 220; i++ {
		at := base.Add(time.Duration(i%24) * time.Hour)
		s := sampleAt("svc", "op", at, 100, 1, 1e6, 2e6, 3e6)
		if err := store.Observe(ctx, "t1", s); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	b, ok := store.Reader().Get("t1", "svc", "op", base)
	if !ok {
		t.Fatal("expected baseline entry to exist")
	}
	if b.Warmed {
		t.Fatalf("expected GlobalOnly (Warmed=false), got Warmed=true")
	}
	if !b.Provisional {
		t.Fatalf("expected GlobalOnly to carry Provisional=true")
	}
}

// TestBaseline_WarmState: both global and the queried season slot warmed.
func TestBaseline_WarmState(t *testing.T) {
	cfg := testConfig()
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 250; i++ {
		s := sampleAt("svc", "op", base, 100, 1, 1e6, 2e6, 3e6)
		if err := store.Observe(ctx, "t1", s); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	b, ok := store.Reader().Get("t1", "svc", "op", base)
	if !ok {
		t.Fatal("expected baseline entry to exist")
	}
	if !b.Warmed || b.Provisional {
		t.Fatalf("expected Warm (Warmed=true, Provisional=false), got Warmed=%v Provisional=%v", b.Warmed, b.Provisional)
	}
}

// TestBaseline_ProvisionalByDecree: global slot still Cold (few samples) but
// now-firstSeen exceeds max_cold_start (24h) -> forced Warm, Provisional=true.
func TestBaseline_ProvisionalByDecree(t *testing.T) {
	cfg := testConfig()
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	firstSeen := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

	s := sampleAt("svc", "op", firstSeen, 100, 1, 1e6, 2e6, 3e6)
	if err := store.Observe(ctx, "t1", s); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	later := firstSeen.Add(25 * time.Hour) // > max_cold_start (24h)
	b, ok := store.Reader().Get("t1", "svc", "op", later)
	if !ok {
		t.Fatal("expected baseline entry to exist")
	}
	if !b.Warmed || !b.Provisional {
		t.Fatalf("expected ProvisionalByDecree (Warmed=true, Provisional=true), got Warmed=%v Provisional=%v", b.Warmed, b.Provisional)
	}

	// Still Cold at 23h (not yet past max_cold_start).
	stillCold := firstSeen.Add(23 * time.Hour)
	b2, ok := store.Reader().Get("t1", "svc", "op", stillCold)
	if !ok {
		t.Fatal("expected baseline entry to exist")
	}
	if b2.Warmed || b2.Provisional {
		t.Fatalf("expected still Cold at 23h, got Warmed=%v Provisional=%v", b2.Warmed, b2.Provisional)
	}
}

// TestBaseline_ProvisionalCapsScore verifies the FR-F05-4 consequence that a
// Provisional incident's Score is capped at 0.69 downstream (see
// grouper_test.go for the Incident-level assertion); here we assert the
// Event itself is stamped Provisional so the Grouper has the signal to cap.
func TestBaseline_ProvisionalEventStamp(t *testing.T) {
	cfg := testConfig()
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

	const baseP95 = uint64(100 * time.Millisecond) // 100ms, realistic latency scale
	for i := 0; i < 220; i++ {
		at := base.Add(time.Duration(i%24) * time.Hour)
		s := sampleAt("svc", "op", at, 100, 1, 1e6, baseP95, baseP95)
		store.Observe(ctx, "t1", s)
	}

	det := NewLatencyShiftDetector(cfg)
	// Breach large enough to survive the widened (x1.5 ratio, x1.5 abs-delta)
	// GlobalOnly threshold: widened ratio 1.5*1.5=2.25 and widened delta
	// 50ms*1.5=75ms both need to hold against a 100ms baseline, so 300ms
	// clears both with margin.
	obsP95 := uint64(300 * time.Millisecond)
	in := EvalInput{
		Now:       base,
		Window:    model.Window{Start: base, End: base.Add(90 * time.Second)},
		Baselines: store.Reader(),
		Samples: []model.REDSample{
			sampleAt("svc", "op", base, 100, 0, 1e6, obsP95, obsP95),
		},
	}
	// debounce_ticks=2: first tick arms, second confirms.
	if _, err := det.Evaluate(ctx, "t1", in); err != nil {
		t.Fatalf("Evaluate (tick1): %v", err)
	}
	events, err := det.Evaluate(ctx, "t1", in)
	if err != nil {
		t.Fatalf("Evaluate (tick2): %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event on confirming tick, got %d", len(events))
	}
	if !events[0].Provisional {
		t.Errorf("expected event from GlobalOnly baseline to carry Provisional=true")
	}
}

// TestBaseline_EWMAConvergence: a step-function input should converge the
// EWMA toward the new value within the expected number of samples for the
// configured alpha (F05 §7).
func TestBaseline_EWMAConvergence(t *testing.T) {
	cfg := testConfig()
	cfg.Baseline.EWMAAlpha = 0.2
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

	// Step 1: converge error rate toward 0 with error-free samples.
	for i := 0; i < 250; i++ {
		store.Observe(ctx, "t1", sampleAt("svc", "op", base, 100, 0, 1e6, 2e6, 3e6))
	}
	b, _ := store.Reader().Get("t1", "svc", "op", base)
	if b.ErrorEWMA > 0.01 {
		t.Fatalf("expected ErrorEWMA near 0 after warm-up on error-free samples, got %v", b.ErrorEWMA)
	}

	// Step 2: step input to a new error rate (0.5), track convergence.
	// With alpha=0.2, after n samples EWMA = target*(1-(1-alpha)^n).
	// n=20 -> 1-0.8^20 ≈ 0.988 -> expect within ~2% of target.
	for i := 0; i < 20; i++ {
		store.Observe(ctx, "t1", sampleAt("svc", "op", base, 100, 50, 1e6, 2e6, 3e6)) // errRate 0.5
	}
	b2, _ := store.Reader().Get("t1", "svc", "op", base)
	target := 0.5
	expected := target * (1 - math.Pow(1-cfg.Baseline.EWMAAlpha, 20))
	if math.Abs(b2.ErrorEWMA-expected) > 0.02 {
		t.Fatalf("EWMA convergence off: got %v, want ~%v (target %v after 20 samples at alpha=%v)", b2.ErrorEWMA, expected, target, cfg.Baseline.EWMAAlpha)
	}
}

// TestBaseline_CardinalityCapEviction: at max_keys, the lowest-RPSEWMA key
// among the oldest-UpdatedAt decile is evicted (DR-14 §14.2), not simply the
// single oldest key. With MaxKeys=10, the decile-of-the-oldest is
// len/10+1 = 2 keys, so the two oldest keys (svc0 = high traffic,
// svc1 = low traffic) are the only eviction candidates: svc1 must be
// evicted, protecting the high-traffic svc0 despite it being strictly
// older.
func TestBaseline_CardinalityCapEviction(t *testing.T) {
	cfg := testConfig()
	cfg.Baseline.MaxKeys = 10
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

	// svc0: oldest, high traffic. svc1..svc9: progressively newer, low
	// traffic. Only svc0/svc1 fall in the 2-key oldest decile.
	store.Observe(ctx, "t1", sampleAt("svc0", "op", base, 1000, 0, 1e6, 2e6, 3e6))
	for i := 1; i < 10; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		store.Observe(ctx, "t1", sampleAt(svcName(i), "op", at, 5, 0, 1e6, 2e6, 3e6))
	}

	if stats := store.Stats(); stats.Keys != 10 {
		t.Fatalf("expected 10 keys before eviction, got %d", stats.Keys)
	}

	// 11th distinct key forces eviction (MaxKeys=10).
	store.Observe(ctx, "t1", sampleAt("svc10", "op", base.Add(10*time.Second), 5, 0, 1e6, 2e6, 3e6))

	stats := store.Stats()
	if stats.Keys != 10 {
		t.Fatalf("expected key count to stay capped at 10 after eviction, got %d", stats.Keys)
	}
	if stats.EvictedKeys != 1 {
		t.Fatalf("expected EvictedKeys=1, got %d", stats.EvictedKeys)
	}
	if _, ok := store.Reader().Get("t1", "svc0", "op", base); !ok {
		t.Errorf("expected high-traffic svc0 to survive cardinality eviction despite being oldest")
	}
	if _, ok := store.Reader().Get("t1", "svc1", "op", base); ok {
		t.Errorf("expected low-traffic svc1 (within the oldest decile) to be evicted instead")
	}
}

func svcName(i int) string { return "svc" + string(rune('0'+i)) }

// TestBaseline_EvictedKeyRestartsCold: a key evicted and later re-seen
// restarts cold (DR-14 §14.2).
func TestBaseline_EvictedKeyRestartsCold(t *testing.T) {
	cfg := testConfig()
	cfg.Baseline.MaxKeys = 1
	store := NewBaselineStore(cfg)
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 250; i++ {
		store.Observe(ctx, "t1", sampleAt("svcA", "op", base, 100, 0, 1e6, 2e6, 3e6))
	}
	b, _ := store.Reader().Get("t1", "svcA", "op", base)
	if !b.Warmed {
		t.Fatal("expected svcA warm before eviction")
	}

	// New key evicts svcA (MaxKeys=1).
	store.Observe(ctx, "t1", sampleAt("svcB", "op", base, 5, 0, 1e6, 2e6, 3e6))
	if _, ok := store.Reader().Get("t1", "svcA", "op", base); ok {
		t.Fatal("expected svcA to have been evicted")
	}

	// Re-observe svcA: must restart cold, not resume warm state.
	store.Observe(ctx, "t1", sampleAt("svcA", "op", base, 100, 0, 1e6, 2e6, 3e6))
	b2, ok := store.Reader().Get("t1", "svcA", "op", base)
	if !ok {
		t.Fatal("expected svcA entry after re-observe")
	}
	if b2.Warmed {
		t.Fatal("expected re-observed (evicted) svcA to restart Cold, not resume Warm")
	}
}

func TestDefaultConfig_MatchesRegister(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"EvalInterval", cfg.EvalInterval, 30 * time.Second},
		{"EvalWindow", cfg.EvalWindow, 90 * time.Second},
		{"DebounceTicks", cfg.DebounceTicks, 2},
		{"Baseline.WarmupSamples", cfg.Baseline.WarmupSamples, uint32(200)},
		{"Baseline.WarmupSamplesPerBucket", cfg.Baseline.WarmupSamplesPerBucket, uint32(30)},
		{"Baseline.ColdStartMultiplier", cfg.Baseline.ColdStartMultiplier, 1.5},
		{"Baseline.MaxColdStart", cfg.Baseline.MaxColdStart, 24 * time.Hour},
		{"Baseline.MaxKeys", cfg.Baseline.MaxKeys, 10000},
		{"Baseline.MaxKeysGlobal", cfg.Baseline.MaxKeysGlobal, 20000},
		{"Baseline.EWMAAlpha", cfg.Baseline.EWMAAlpha, 0.2},
		{"Thresholds.LatencyRatio", cfg.Thresholds.LatencyRatio, 1.5},
		{"Thresholds.ErrorRateDelta", cfg.Thresholds.ErrorRateDelta, 0.05},
		{"Thresholds.ErrorBurstRatio", cfg.Thresholds.ErrorBurstRatio, 3.0},
		{"Thresholds.ThroughputDropRatio", cfg.Thresholds.ThroughputDropRatio, 0.5},
		{"Thresholds.MinCalls", cfg.Thresholds.MinCalls, uint64(20)},
		{"Thresholds.MinEventScore", cfg.Thresholds.MinEventScore, 0.55},
		{"Grouping.Window", cfg.Grouping.Window, 5 * time.Minute},
		{"Grouping.TopologyHops", cfg.Grouping.TopologyHops, 2},
		{"Grouping.MaxOpenIncidents", cfg.Grouping.MaxOpenIncidents, 200},
		{"Grouping.DedupeTTL", cfg.Grouping.DedupeTTL, 30 * time.Minute},
		{"DeployMarkers.CorrelationWindow", cfg.DeployMarkers.CorrelationWindow, 30 * time.Minute},
		{"DeployMarkers.Settle", cfg.DeployMarkers.Settle, 2 * time.Minute},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (DR-14 §14.9)", c.name, c.got, c.want)
		}
	}
}
