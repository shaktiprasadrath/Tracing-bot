package eval

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// Mode is DR-36 §36.1, verbatim.
type Mode uint8

const (
	ModeOffline Mode = 1
	ModeLive    Mode = 2
)

// Isolation is DR-36 §36.1, verbatim. IsolationShared refuses
// Parallelism > 1 (DR-36 §36.3).
type Isolation uint8

const (
	IsolationProcess Isolation = 1
	IsolationShared  Isolation = 2
)

// Runner is DR-36 §36.1, verbatim.
type Runner interface {
	Run(ctx context.Context, opts RunOptions) (Report, error)
	List(ctx context.Context, dir string) ([]ScenarioSpec, error)
	Stats() Stats
}

// RunOptions is DR-36 §36.1, verbatim. Reasoner is typed model.ReasonerKind
// per DR-4 (never redefine model DTOs); the register's own text names
// `model.ReasonerKind` explicitly.
type RunOptions struct {
	Tenant             model.TenantID
	ScenariosDir       string
	Select             []string // scenario ids; empty = all
	Mode               Mode     // ModeOffline is the default and the only mode CI runs
	Isolation          Isolation
	Parallelism        int // default 4 with IsolationProcess
	Seed               int64
	Clock              model.Clock // eval.VirtualClock in ModeOffline (DR-31)
	Reasoner           model.ReasonerKind
	RegressionBaseline string // path to a stored Report
	FailOnRegression   bool
	OutputDir          string
	Timeout            time.Duration
}

// VirtualClock is DR-31, verbatim: advances only when every registered
// Barrier reports Pending() == 0, so a 30-minute scenario runs in seconds
// and is bit-reproducible (DR-36 §36.3). It is expected to also satisfy
// model.Clock (RunOptions.Clock) once implemented; that implementation is
// business logic and intentionally left as a TODO in this types-only
// scaffold.
// TODO(DR-31): implement model.Clock on VirtualClock via Barrier quiescence.
type VirtualClock struct {
	Start time.Time
}

func (c *VirtualClock) Advance(d time.Duration)                   { panic("not implemented") }
func (c *VirtualClock) AwaitQuiescence(ctx context.Context) error { panic("not implemented") }
func (c *VirtualClock) Register(b model.Barrier)                  { panic("not implemented") }

// FixtureRef is a ScenarioSpec.Fixture / BaselineWarmup value: "OTLP/NDJSON
// span bundle + optional log and metric bundles for the file adapters"
// (DR-36 §36.4). The register describes it in prose only.
// TODO(DR-36): confirm field list against the fixture file format.
type FixtureRef struct {
	Path        string
	LogsPath    string
	MetricsPath string
}

// ClockSpec is ScenarioSpec.Clock: "{start, fault_at, end} — all virtual"
// (DR-36 §36.4), verbatim field trio.
type ClockSpec struct {
	Start   time.Time
	FaultAt time.Time
	End     time.Time
}

// FaultSpec is ScenarioSpec.Faults (ModeLive only). Not given a shape in
// the register beyond "ModeLive only" and DR-36 §36.7's five closed
// model.ActionType values applied through remediate.Guard.
// TODO(DR-36): confirm field list; must reduce to a model.ActionProposal
// dispatched via remediate.Guard with ProposedBy = "eval:<runID>".
type FaultSpec struct {
	Type   model.ActionType
	Target model.ActionTarget
}

// ScenarioSpec is DR-36 §36.4, verbatim.
type ScenarioSpec struct {
	ID, Title, Description string
	Tenant                 model.TenantID
	Fixture                FixtureRef  // OTLP/NDJSON span bundle + optional log/metric bundles
	BaselineWarmup         *FixtureRef // or an inline BaselineSnapshot
	Clock                  ClockSpec   // {start, fault_at, end} — all virtual
	Faults                 []FaultSpec // ModeLive only
	Expect                 Expectation
}

// Expectation is DR-36 §36.4, verbatim. EvidenceCategory is
// model.EvidenceCategory (DR-36's own header note: "defined once, here
// [model], rather than duplicated in internal/eval" — see
// internal/model/anomaly.go).
type Expectation struct {
	IncidentWithin     time.Duration
	EpicenterService   string
	RootCauseCategory  model.HypothesisCategory
	RootCauseComponent string
	ExpectedEvidence   []model.EvidenceCategory // CLOSED enum — makes EvidenceRecall computable
	MaxCostMicroUSD    int64
	MustNotPage        bool
}

// Result is one scenario's scored outcome (DR-36 §36.5: Top-1/Top-3,
// PartialCredit, EvidencePrecision/Recall, DetectLatencyMillis,
// TimeToRCAMillis, CostMicroUSD, ReasonerKind). The register lists these as
// fields the scorer produces and that `01 §5.1`'s eval_result table /
// `02`'s eval.Result gain, but never prints the struct verbatim.
// TODO(DR-36): confirm exact field names against the eval_result DDL.
type Result struct {
	ScenarioID          string
	Top1                bool
	Top3                bool
	PartialCredit       float64
	EvidencePrecision   float64
	EvidenceRecall      float64
	EvidenceExpected    bool // true iff spec.Expect.ExpectedEvidence was non-empty for this scenario — see Report.EvidenceScenariosCount's doc comment for why this matters.
	DetectLatencyMillis int64
	TimeToRCAMillis     int64
	CostMicroUSD        int64
	ReasonerKind        model.ReasonerKind
	Investigation       *model.Investigation
	Error               string
}

// Report is Runner.Run's return type: the aggregate of every scenario
// Result plus the CI-gate verdicts (DR-36 §36.6). Field list reconstructed
// minimally; the register specifies the gates' thresholds but not this
// struct's shape.
//
// w13 addition (additive only, per this wave's scope): MeanTimeToRCAMillis/
// MeanEvidencePrecision/MeanEvidenceRecall/Seed close the gap between this
// struct and F11 §4.2's ReportSummary (DR-36 cites F11's data model but
// never itself restates a ReportSummary shape) — the report builder
// (report.go) needs somewhere to put these, and the register's own note on
// Report ("field list... TODO") explicitly permits reconstruction. See
// docs/reports/w13-eval.md.
type Report struct {
	RunID                 string
	StartedAt             time.Time
	FinishedAt            time.Time
	Mode                  Mode
	Reasoner              model.ReasonerKind
	Seed                  int64
	Results               []Result
	Total                 int
	Passed                int
	Top1Accuracy          float64
	Top3Accuracy          float64
	MeanTimeToRCAMillis   int64
	MeanEvidencePrecision float64
	MeanEvidenceRecall    float64
	// EvidenceScenariosCount is the number of Results with EvidenceExpected
	// == true (i.e. spec.Expect.ExpectedEvidence was non-empty). scoreEvidence
	// scores an empty expectation vacuously as 1.0/1.0 "nothing to be
	// imprecise or incomplete about" (documented convention, score.go) — so
	// when EvidenceScenariosCount == 0, MeanEvidencePrecision/
	// MeanEvidenceRecall are 1.0 by construction and measure nothing: no
	// fixture in the run actually exercised evidence scoring. This is
	// currently always true end-to-end because rca.Engine never populates
	// Step.EvidenceIDs (see docs/reports/w13-eval.md, docs/reports/
	// w14-review-eval.md) — surfaced here so report.md/report.json never let
	// a reader mistake the vacuous 1.0/1.0 for a validated measurement.
	EvidenceScenariosCount int
	// MedianCostMicroUSD / MaxCostMicroUSD are FR-F11-11 / DR-36 §36.6's cost
	// gate inputs (median <= $0.08, max <= $0.50 per investigation),
	// computed over Results[].CostMicroUSD.
	MedianCostMicroUSD int64
	MaxCostMicroUSD    int64
	GatesPassed        bool
	FailedGates        []string
	RegressionDiff     map[string]float64
}

// Stats is Runner.Stats' return type. Not given a shape in the register;
// reconstructed minimally.
// TODO(DR-36): confirm against any published /v1/eval/stats response.
type Stats struct {
	RunsTotal      int
	LastRunAt      time.Time
	LastGatesPass  bool
	ScenariosCount int
}
