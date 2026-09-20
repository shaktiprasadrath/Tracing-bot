package eval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"traceiq/internal/rca"
)

// runner is this wave's Runner implementation (DR-36 §36.1's interface,
// verbatim). Scope for w13 (see docs/reports/w13-eval.md): ModeOffline
// only, fixture-replay driven directly through rca.Engine + rules.Reasoner
// (driver.go) rather than through ingest.Receiver (deferred — no
// ingest/anomaly wiring exists yet for eval to drive). Scenarios run
// sequentially: each already gets fresh in-memory Journal/ObjectStore/
// ToolRegistry state per call (driver.go), which is IsolationProcess's
// actual guarantee ("no shared mutable state across scenarios") even
// without a real worker pool; true concurrent Parallelism > 1 execution is
// deferred, not required for this wave's determinism/gate acceptance
// criteria.
type runner struct {
	reasoner rca.Reasoner

	mu    sync.Mutex
	stats Stats
}

// NewRunner constructs this wave's Runner. reasoner is the rca.Reasoner
// every scenario is driven through (see driver.go's investigate doc
// comment for why this is caller-injected rather than eval constructing
// rules.New() itself); it MUST be non-nil for Run to succeed. Its
// Kind() MUST agree with RunOptions.Reasoner on every Run call — a
// mismatch is a caller bug, not a scenario failure, and is reported as
// such.
func NewRunner(reasoner rca.Reasoner) Runner { return &runner{reasoner: reasoner} }

func (r *runner) List(_ context.Context, dir string) ([]ScenarioSpec, error) {
	return List(dir, nil)
}

func (r *runner) Run(ctx context.Context, opts RunOptions) (Report, error) {
	if opts.Mode == 0 {
		opts.Mode = ModeOffline
	}
	if opts.Mode != ModeOffline {
		return Report{}, fmt.Errorf("eval: %s is out of scope this wave (w13) — ModeOffline only", "ModeLive")
	}
	if r.reasoner == nil {
		return Report{}, fmt.Errorf("eval: NewRunner was constructed with a nil Reasoner")
	}
	if opts.Reasoner == "" {
		opts.Reasoner = r.reasoner.Kind()
	}
	if opts.Reasoner != r.reasoner.Kind() {
		return Report{}, fmt.Errorf("eval: RunOptions.Reasoner=%q does not match the injected Reasoner.Kind()=%q", opts.Reasoner, r.reasoner.Kind())
	}
	if opts.Isolation == 0 {
		opts.Isolation = IsolationProcess
	}
	if opts.Isolation == IsolationShared && opts.Parallelism > 1 {
		return Report{}, fmt.Errorf("eval: IsolationShared refuses Parallelism > 1 (got %d)", opts.Parallelism)
	}

	specs, err := List(opts.ScenariosDir, opts.Select)
	if err != nil {
		return Report{}, err
	}

	started := time.Now()
	results := make([]Result, len(specs))
	for i, spec := range specs {
		results[i] = runOne(ctx, spec, opts, r.reasoner)
	}
	finished := time.Now()

	rep := buildReport(newRunID(), started, finished, opts, results)

	baseline, err := loadBaselineReport(opts.RegressionBaseline)
	if err != nil {
		return rep, err
	}
	gateErr := checkGates(&rep, opts, baseline)

	if opts.OutputDir != "" {
		if werr := writeReport(opts.OutputDir, rep); werr != nil {
			if gateErr == nil {
				gateErr = werr
			}
		}
	}

	r.mu.Lock()
	r.stats.RunsTotal++
	r.stats.LastRunAt = finished
	r.stats.LastGatesPass = rep.GatesPassed
	r.stats.ScenariosCount = len(specs)
	r.mu.Unlock()

	return rep, gateErr
}

func (r *runner) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// runOne runs and scores a single scenario (F11 §4.4's runOne pseudocode,
// FR-F11-4/-5/-6/-10), scoped to ModeOffline's fixture-replay driver.
func runOne(ctx context.Context, spec ScenarioSpec, opts RunOptions, reasoner rca.Reasoner) Result {
	inv, incident, err := investigate(ctx, spec, opts.Tenant, reasoner)
	if err != nil {
		return Result{ScenarioID: spec.ID, ReasonerKind: opts.Reasoner, Error: err.Error()}
	}

	top1, top3, partial := scoreRootCause(inv.Hypotheses, spec.Expect)
	precision, recall := scoreEvidence(inv.Steps, nil, spec.Expect.ExpectedEvidence)

	// TimeToRCAMillis (FR-F11-5) is measured end-to-end against the
	// Engine's own clock (StartedAt -> EndedAt), both real wall-clock
	// timestamps from the same investigation run. This wave deliberately
	// does NOT measure from Incident.FirstSeen: that field is authored
	// directly from the ScenarioSpec's virtual ClockSpec (driver.go's
	// buildIncident), which can be arbitrarily far from the real "now" a
	// non-virtual-clock Engine.Investigate stamps StartedAt/EndedAt with —
	// eval.VirtualClock (DR-31) is the documented fix, and is out of scope
	// this wave (see docs/reports/w13-eval.md).
	timeToRCA := inv.EndedAt.Sub(inv.StartedAt)
	detectLatency := inv.StartedAt.Sub(incident.FirstSeen)
	if detectLatency < 0 {
		detectLatency = 0
	}

	return Result{
		ScenarioID:          spec.ID,
		Top1:                top1,
		Top3:                top3,
		PartialCredit:       partial,
		EvidencePrecision:   precision,
		EvidenceRecall:      recall,
		EvidenceExpected:    len(spec.Expect.ExpectedEvidence) > 0,
		DetectLatencyMillis: detectLatency.Milliseconds(),
		TimeToRCAMillis:     timeToRCA.Milliseconds(),
		CostMicroUSD:        inv.Spend.CostMicroUSD,
		ReasonerKind:        inv.ReasonerKind,
		Investigation:       &inv,
		Error:               inv.Error,
	}
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "run-" + hex.EncodeToString(b[:])
}
