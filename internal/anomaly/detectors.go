package anomaly

import (
	"context"
	"sync"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/topology"
)

// redGroup is one (service, operation)'s aggregated observation over the
// samples handed to a single Evaluate call.
type redGroup struct {
	service, operation string
	calls, errors      uint64
	p50, p95, p99      uint64 // most recent bucket's quantiles in the window
}

func groupSamples(samples []model.REDSample) map[[2]string]*redGroup {
	out := make(map[[2]string]*redGroup)
	for _, s := range samples {
		k := [2]string{s.Service, s.Operation}
		g, ok := out[k]
		if !ok {
			g = &redGroup{service: s.Service, operation: s.Operation}
			out[k] = g
		}
		g.calls += s.Calls
		g.errors += s.Errors
		g.p50, g.p95, g.p99 = s.Q.P50Nanos, s.Q.P95Nanos, s.Q.P99Nanos
	}
	return out
}

func baselineKeyStr(tid model.TenantID, service, operation string) string {
	return string(tid) + "|" + service + "|" + operation
}

// --- latency_shift (FR-F05-5) ---

type LatencyShiftDetector struct {
	cfg         Config
	mu          sync.Mutex
	consecutive map[string]int
}

func NewLatencyShiftDetector(cfg Config) *LatencyShiftDetector {
	return &LatencyShiftDetector{cfg: cfg, consecutive: make(map[string]int)}
}

func (d *LatencyShiftDetector) Kind() Kind { return KindLatencyShift }

func (d *LatencyShiftDetector) Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var events []model.AnomalyEvent
	for _, g := range groupSamples(in.Samples) {
		key := baselineKeyStr(tid, g.service, g.operation)
		base, ok := in.Baselines.Get(tid, g.service, g.operation, in.Now)
		if !ok || (!base.Warmed && !base.Provisional) {
			d.consecutive[key] = 0
			continue // unknown or Cold: suppressed (FR-F05-4)
		}
		widen := !base.Warmed && base.Provisional // GlobalOnly
		ratioTh := d.cfg.Thresholds.LatencyRatio
		deltaTh := d.cfg.Thresholds.LatencyAbsDeltaNanos
		if widen {
			ratioTh *= d.cfg.Baseline.ColdStartMultiplier
			deltaTh *= d.cfg.Baseline.ColdStartMultiplier
		}

		obsP95 := float64(g.p95)
		baseP95 := float64(base.Q.P95Nanos)
		delta := obsP95 - baseP95
		breach := baseP95 > 0 && obsP95 >= baseP95*ratioTh && delta >= deltaTh && g.calls >= d.cfg.Thresholds.MinCalls

		if !breach {
			d.consecutive[key] = 0
			continue
		}
		d.consecutive[key]++
		if d.consecutive[key] < d.cfg.DebounceTicks {
			continue
		}
		d.consecutive[key] = 0

		ratio := obsP95 / baseP95
		score := clamp01(0.5*min1((ratio-1)/1.0) + 0.5*min1(delta/(4*d.cfg.Thresholds.LatencyAbsDeltaNanos)))
		ev := model.AnomalyEvent{
			Tenant: tid, Kind: model.AnomalyLatencyShift, DetectorID: "latency_shift/v1",
			Service: g.service, Operation: g.operation,
			WindowStart: in.Window.Start, WindowEnd: in.Window.End,
			Observed: obsP95, Baseline: baseP95, Deviation: ratio,
			Score: score, Provisional: base.Provisional,
			CreatedAt: in.Now,
		}
		applyDeployEnrichment(ctx, tid, in.Deploys, d.cfg, &ev)
		// DR-14 §14.4: an event with Score < min_event_score is still
		// "recorded in anomaly_event" — only Grouper-forwarding is gated on
		// score, and that gate belongs to whichever caller wires Evaluate's
		// output to Grouper.Add (NEEDS_CONTEXT: no such orchestrator exists
		// yet in this batch). Evaluate is documented pure/no-I/O, so it is
		// the only channel through which a future caller can persist a
		// sub-threshold event; filtering it out here would make that
		// persistence impossible. Fixed from an earlier version that
		// dropped these events outright (bug found via boundary TDD tests).
		events = append(events, ev)
	}
	return events, nil
}

var _ Detector = (*LatencyShiftDetector)(nil)

// --- error_burst (FR-F05-6) ---

type ErrorBurstDetector struct {
	cfg         Config
	mu          sync.Mutex
	consecutive map[string]int
}

func NewErrorBurstDetector(cfg Config) *ErrorBurstDetector {
	return &ErrorBurstDetector{cfg: cfg, consecutive: make(map[string]int)}
}

func (d *ErrorBurstDetector) Kind() Kind { return KindErrorBurst }

func (d *ErrorBurstDetector) Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var events []model.AnomalyEvent
	for _, g := range groupSamples(in.Samples) {
		key := baselineKeyStr(tid, g.service, g.operation)
		base, ok := in.Baselines.Get(tid, g.service, g.operation, in.Now)
		if !ok || (!base.Warmed && !base.Provisional) {
			d.consecutive[key] = 0
			continue
		}
		widen := !base.Warmed && base.Provisional
		rateDeltaTh := d.cfg.Thresholds.ErrorRateDelta
		ratioTh := d.cfg.Thresholds.ErrorBurstRatio
		if widen {
			rateDeltaTh *= d.cfg.Baseline.ColdStartMultiplier
			ratioTh *= d.cfg.Baseline.ColdStartMultiplier
		}

		if g.calls == 0 {
			d.consecutive[key] = 0
			continue
		}
		obsRate := float64(g.errors) / float64(g.calls)
		breach := (obsRate-base.ErrorEWMA) >= rateDeltaTh && obsRate >= base.ErrorEWMA*ratioTh && g.calls >= d.cfg.Thresholds.MinCalls

		if !breach {
			d.consecutive[key] = 0
			continue
		}
		d.consecutive[key]++
		if d.consecutive[key] < d.cfg.DebounceTicks {
			continue
		}
		d.consecutive[key] = 0

		score := clamp01(0.5*min1((obsRate-base.ErrorEWMA)/0.20) + 0.5*min1(obsRate))
		ev := model.AnomalyEvent{
			Tenant: tid, Kind: model.AnomalyErrorBurst, DetectorID: "error_burst/v1",
			Service: g.service, Operation: g.operation,
			WindowStart: in.Window.Start, WindowEnd: in.Window.End,
			Observed: obsRate, Baseline: base.ErrorEWMA, Deviation: obsRate - base.ErrorEWMA,
			Score: score, Provisional: base.Provisional,
			CreatedAt: in.Now,
		}
		applyDeployEnrichment(ctx, tid, in.Deploys, d.cfg, &ev)
		// See LatencyShiftDetector.Evaluate's comment: sub-threshold events
		// are still returned (DR-14 §14.4), not dropped here.
		events = append(events, ev)
	}
	return events, nil
}

var _ Detector = (*ErrorBurstDetector)(nil)

// --- throughput_drop (FR-F05-13) ---

type ThroughputDropDetector struct {
	cfg         Config
	mu          sync.Mutex
	consecutive map[string]int
}

func NewThroughputDropDetector(cfg Config) *ThroughputDropDetector {
	return &ThroughputDropDetector{cfg: cfg, consecutive: make(map[string]int)}
}

func (d *ThroughputDropDetector) Kind() Kind { return KindThroughputDrop }

func (d *ThroughputDropDetector) Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	windowSeconds := in.Window.End.Sub(in.Window.Start).Seconds()
	if windowSeconds <= 0 {
		windowSeconds = d.cfg.EvalWindow.Seconds()
	}

	var events []model.AnomalyEvent
	for _, g := range groupSamples(in.Samples) {
		key := baselineKeyStr(tid, g.service, g.operation)
		base, ok := in.Baselines.Get(tid, g.service, g.operation, in.Now)
		if !ok || (!base.Warmed && !base.Provisional) {
			d.consecutive[key] = 0
			continue
		}
		if base.RPSEWMA < d.cfg.Baseline.MinRPS {
			d.consecutive[key] = 0
			continue
		}
		obsRPS := float64(g.calls) / windowSeconds
		breach := obsRPS <= base.RPSEWMA*d.cfg.Thresholds.ThroughputDropRatio

		if !breach {
			d.consecutive[key] = 0
			continue
		}
		d.consecutive[key]++
		if d.consecutive[key] < d.cfg.DebounceTicks {
			continue
		}
		d.consecutive[key] = 0

		score := clamp01(1 - obsRPS/base.RPSEWMA)
		ev := model.AnomalyEvent{
			Tenant: tid, Kind: model.AnomalyThroughputDrop, DetectorID: "throughput_drop/v1",
			Service: g.service, Operation: g.operation,
			WindowStart: in.Window.Start, WindowEnd: in.Window.End,
			Observed: obsRPS, Baseline: base.RPSEWMA, Deviation: base.RPSEWMA - obsRPS,
			Score: score, Provisional: base.Provisional,
			CreatedAt: in.Now,
		}
		// See LatencyShiftDetector.Evaluate's comment: sub-threshold events
		// are still returned (DR-14 §14.4), not dropped here.
		events = append(events, ev)
	}
	return events, nil
}

var _ Detector = (*ThroughputDropDetector)(nil)

// --- new_error_signature (FR-F05-7) ---

type NewErrorSignatureDetector struct {
	cfg     Config
	mu      sync.Mutex
	emitted map[string]bool
}

func NewNewErrorSignatureDetector(cfg Config) *NewErrorSignatureDetector {
	return &NewErrorSignatureDetector{cfg: cfg, emitted: make(map[string]bool)}
}

func (d *NewErrorSignatureDetector) Kind() Kind { return KindNewErrorSignature }

func (d *NewErrorSignatureDetector) Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	windowDur := in.Window.End.Sub(in.Window.Start)
	if windowDur <= 0 {
		windowDur = d.cfg.EvalWindow
	}

	var events []model.AnomalyEvent
	for _, sig := range in.ErrorSigs {
		if sig.Tenant != tid {
			continue
		}
		dedupeKey := baselineKeyStr(tid, sig.Service, sig.ID)
		if d.emitted[dedupeKey] {
			continue
		}
		age := in.Now.Sub(sig.FirstSeen)
		if age < 0 || age > windowDur {
			continue // not newly first-seen in this tick's window
		}
		if sig.Samples < d.cfg.Thresholds.NewErrorSigMinCalls {
			continue
		}
		d.emitted[dedupeKey] = true
		score := clamp01(0.4 + 0.6*min1(float64(sig.Samples)/50))
		ev := model.AnomalyEvent{
			Tenant: tid, Kind: model.AnomalyNewErrorSignature, DetectorID: "new_error_signature/v1",
			Service:     sig.Service,
			ErrorSigID:  sig.ID,
			WindowStart: in.Window.Start, WindowEnd: in.Window.End,
			Observed: float64(sig.Samples), Score: score,
			CreatedAt: in.Now,
		}
		// See LatencyShiftDetector.Evaluate's comment: sub-threshold events
		// are still returned (DR-14 §14.4), not dropped here.
		events = append(events, ev)
	}
	return events, nil
}

var _ Detector = (*NewErrorSignatureDetector)(nil)

// --- topology_change (FR-F05-8) ---

// TopologyChangeDetector unions a non-blocking drain of topology.Graph's
// Changes() channel with a durable reconciliation read through
// EdgeMetaReader every tick (DR-14 §14.7). The channel is supplied at
// construction (typically graph.Changes()); this package never imports the
// full topology.Graph interface to obtain it, keeping the detector testable
// against a synthetic channel.
type TopologyChangeDetector struct {
	cfg      Config
	changes  <-chan topology.ChangeEvent
	edgeMeta EdgeMetaReader

	mu sync.Mutex
	// lastReconcile and emitted are keyed per tenant: like every other
	// detector in this package (see baselineKeyStr's tid-prefixed keys),
	// a single TopologyChangeDetector instance must be safe to reuse
	// across tenants even though the channel supplied at construction is
	// typically a single tenant-scoped topology.LiveGraph.Changes(). Two
	// tenants can otherwise legitimately share the same Edge.ID (edgeIDFor
	// hashes only caller/callee/protocol, not tenant), so an unscoped
	// dedupe map would let tenant A's first sighting of "frontend->backend"
	// permanently suppress tenant B's genuinely new edge of the same shape.
	// Bug found and fixed in the w12 review pass.
	lastReconcile map[model.TenantID]time.Time
	emitted       map[string]time.Time // dedupeKey -> emitted-at
}

func NewTopologyChangeDetector(cfg Config, changes <-chan topology.ChangeEvent, edgeMeta EdgeMetaReader) *TopologyChangeDetector {
	return &TopologyChangeDetector{
		cfg: cfg, changes: changes, edgeMeta: edgeMeta,
		lastReconcile: make(map[model.TenantID]time.Time),
		emitted:       make(map[string]time.Time),
	}
}

func (d *TopologyChangeDetector) Kind() Kind { return KindTopologyChange }

func (d *TopologyChangeDetector) Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var events []model.AnomalyEvent

	drained := 0
drainLoop:
	for d.changes != nil && drained < 256 {
		select {
		case ce, ok := <-d.changes:
			if !ok {
				d.changes = nil
				break drainLoop
			}
			drained++
			if ce.Edge.Tenant != tid {
				continue
			}
			if ev, ok := d.evalChangeEvent(ce, in.Now); ok {
				events = append(events, ev)
			}
		default:
			break drainLoop
		}
	}

	if d.edgeMeta != nil {
		since := d.lastReconcile[tid]
		if since.IsZero() {
			since = in.Now.Add(-d.cfg.EvalInterval)
		}
		edges, err := d.edgeMeta.NewEdgesSince(ctx, tid, since)
		if err == nil {
			for _, e := range edges {
				ce := topology.ChangeEvent{Kind: "NewEdge", Edge: e, Detected: in.Now}
				if ev, ok := d.evalChangeEvent(ce, in.Now); ok {
					events = append(events, ev)
				}
			}
		}
		d.lastReconcile[tid] = in.Now
	}

	return events, nil
}

// evalChangeEvent's local dedupe (DR-14 §14.7: "union and dedupe by
// Edge.ID") only needs to survive the gap between the channel-drain and
// reconciliation-read halves of the *same* logical occurrence seeing the
// same edge; it must not become a permanent, life-of-process suppression,
// or a real edge that genuinely vanishes and later reappears (a distinct,
// legitimate NewEdge occurrence topology.LiveGraph will correctly refire
// per its own newEdgeWindow, per the w10 review's Finding-1 fix) would
// never be reported again by this package. Bounded by grouping.dedupe_ttl
// (30m default) -- long enough to cover both intake paths across several
// eval_interval ticks, short enough to let a real re-occurrence through.
// Entries are pruned lazily on access; cardinality is bounded by the
// number of distinct (tenant, edge, kind) tuples touched within the TTL,
// which tracks live topology size, not an unbounded input.
func (d *TopologyChangeDetector) evalChangeEvent(ce topology.ChangeEvent, now time.Time) (model.AnomalyEvent, bool) {
	dedupeKey := string(ce.Edge.Tenant) + "|" + ce.Edge.ID + "|" + ce.Kind
	if emittedAt, ok := d.emitted[dedupeKey]; ok {
		if now.Sub(emittedAt) < d.cfg.Grouping.DedupeTTL {
			return model.AnomalyEvent{}, false
		}
		delete(d.emitted, dedupeKey)
	}

	var score float64
	switch ce.Kind {
	case "NewEdge":
		if ce.Edge.Calls < d.cfg.Thresholds.NewEdgeMinCalls {
			return model.AnomalyEvent{}, false
		}
		score = 0.50
	case "VanishedEdge":
		if ce.Edge.Calls < d.cfg.Thresholds.VanishedEdgeMinCalls24h {
			return model.AnomalyEvent{}, false
		}
		score = 0.60
	default:
		return model.AnomalyEvent{}, false
	}

	d.emitted[dedupeKey] = now
	return model.AnomalyEvent{
		Tenant: ce.Edge.Tenant, Kind: model.AnomalyTopologyChange, DetectorID: "topology_change/v1",
		Service: ce.Edge.Callee, EdgeID: ce.Edge.ID,
		WindowStart: ce.Detected, WindowEnd: ce.Detected,
		Score: score, CreatedAt: now,
	}, true
}

var _ Detector = (*TopologyChangeDetector)(nil)
