package rca

import (
	"context"
	"time"

	"traceiq/internal/llm"
	"traceiq/internal/model"
	"traceiq/internal/topology"
)

// Engine is DR-15's replacement for F06 §4.3's single-method Engine,
// verbatim.
type Engine interface {
	Investigate(ctx context.Context, tid model.TenantID, inc model.Incident) (model.Investigation, error)
	Get(ctx context.Context, tid model.TenantID, id string) (model.Investigation, error)
	Replay(ctx context.Context, tid model.TenantID, investigationID string, mode ReplayMode) (model.Investigation, error) // DR-18
	Correct(ctx context.Context, tid model.TenantID, investigationID, stepID string, c model.Correction) (model.Investigation, error)
	Abort(ctx context.Context, tid model.TenantID, investigationID, reason string) error
	Stats() Stats
}

// ReplayMode aliases model.ReplayMode (DR-18 §18.2's canonical location) so
// Engine.Replay can be written exactly as DR-15 prints it.
type ReplayMode = model.ReplayMode

const (
	ReplayRecorded = model.ReplayRecorded
	ReplayLiveDiff = model.ReplayLiveDiff
)

// Reasoner is DR-15, verbatim.
type Reasoner interface {
	Kind() model.ReasonerKind // "llm" | "rules"
	NextStep(ctx context.Context, tid model.TenantID, s State) (Proposal, error)
	Conclude(ctx context.Context, tid model.TenantID, s State) (model.Conclusion, error)
}

// State is Reasoner.NextStep/Conclude's state parameter. The register never
// gives an explicit field list.
//
// w11 addition (additive, documented per the TODO below): LastArgs/
// LastResult/LastHypothesisID close a real gap the rules reasoner hit. The
// main loop pseudocode (F06 §4.4) calls "reasoner.evaluate(proposal,
// result)" as a distinct step from NextStep, but DR-15's Reasoner interface
// (verbatim, above) has only NextStep and Conclude — there is no third
// method. Since a Step's persisted form (DR-18 §18.1) deliberately holds
// only ToolResultHash/ToolResultRef, never the body, NextStep cannot
// re-derive a just-dispatched result's content from Investigation.Steps
// alone. The Engine therefore passes the most recent dispatch's args/result
// through State for exactly one subsequent NextStep call (single-use, then
// cleared by the Engine before the call after that): the reasoner's first
// action on seeing a non-nil LastResult is to fold it into the matching
// Hypothesis's Status/PostScore (a pure-reasoning Proposal, Call == nil)
// before proposing the next tool call. This keeps NextStep/Conclude
// otherwise pure functions of Investigation+Incident, and keeps the
// evaluate-then-propose split out of the DR-15 interface itself. See
// docs/reports/w11-rca-rules.md.
type State struct {
	Investigation model.Investigation
	Incident      model.Incident

	LastArgs         *ToolArgs         // the previous step's dispatched args, or nil
	LastResult       *model.ToolResult // the previous step's dispatch result, or nil
	LastHypothesisID string            // Hypothesis.ID the previous Call was testing, or ""
}

// LLMReasoner is rca's llm.Client-backed Reasoner (package doc.go, DR-2's
// cycle-break table: "the Anthropic client lives in internal/llm ...
// rca.LLMReasoner holds an llm.Client rather than rca declaring its own
// Anthropic client type"). Implemented w15; see llm_reasoner.go.
//
// Fields beyond Client are this wave's concretization of DR-34 §34.1's
// binding request shape (model, effort, thinking: adaptive is hardcoded
// since DR-34 gives it no config knob) and F06 §4.4's retry count R.
// Validator/Topo/Sanitizer are optional seams for tests; a nil value gets a
// sane internal default at first use (see llm_reasoner.go's defaulted
// helpers) so `LLMReasoner{Client: fake}` alone is a valid zero-config value.
type LLMReasoner struct {
	Client llm.Client

	Model      string // rca.llm.model, e.g. "claude-opus-5" (DR-34 §34.1)
	Effort     string // low | medium | high; defaults to "medium"
	MaxRetries int    // F06 §4.4's R; 0 means the DR-17 §17.2 default of 2

	Validator SchemaValidator // defaults to newSchemaValidator()
	Topo      TopologyReader  // optional; nil skips the Component-resolves-in-topology check

	Pricing LLMPricing // DR-34 §34.3's cost formula; zero value prices every call at $0
}

// LLMPricing is DR-34 §34.3's `rca.llm.pricing` block (micro-USD per million
// tokens), concretized as a Go type since the register only ever prints it
// as YAML. Used by LLMReasoner to compute Proposal.Usage.CostMicroUSD from
// llm.Response.Usage via the formula DR-34 §34.3 prints verbatim.
type LLMPricing struct {
	InputMicroUSDPerM      int64
	CachedReadMicroUSDPerM int64
	CacheWriteMicroUSDPerM int64
	OutputMicroUSDPerM     int64
}

// Proposal is DR-15, verbatim, plus one w15 addition: Usage. DR-15's shape
// has no field carrying an LLM call's token/cost usage back to the Engine,
// but DR-17's Budget.Charge(tokensIn, cachedIn, tokensOut, cost) has to be
// fed from somewhere, and Reasoner is the only component that ever sees
// llm.Response.Usage. This follows the precedent State.LastArgs/LastResult/
// LastHypothesisID already set (w11, rca.go's State doc comment): an
// additive, documented field closing a real gap rather than leaving a TODO.
// Always the zero Charge{} for the rules reasoner.
type Proposal struct {
	Phase      model.Phase        // Contextualize | Hypothesize | Test | Validate | Report
	Hypotheses []model.Hypothesis // additions or score updates only
	Call       *ToolArgs          // nil when the step is pure reasoning
	Done       bool
	Rationale  string // <= 2000 bytes, DISPLAY ONLY, never reaches a tool or the cluster
	Usage      Charge // w15 addition; zero value for the rules reasoner
}

// Tool is DR-15, verbatim.
type Tool interface {
	Name() model.ToolName
	Schema() ArgSchema // rendered into the model's tool list; also the validator's source
	Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error)
	Cost() ToolCost // declared row/byte/time ceilings, clamped server-side
}

// ArgSchema is Tool.Schema's return type. The register never gives an
// explicit field list; shaped by analogy to llm.ToolSchema, which is
// rendered from it ("rendered into the request's tools array").
// TODO(DR-15): under-specified.
type ArgSchema struct {
	Name        model.ToolName
	Description string
	JSONSchema  []byte
}

// ToolCost is Tool.Cost's return type. The register never gives an explicit
// field list.
// TODO(DR-15): under-specified.
type ToolCost struct {
	MaxRows      int
	MaxBytes     int64
	MaxWallClock time.Duration
}

// ToolRegistry is DR-15, verbatim.
type ToolRegistry interface {
	Get(n model.ToolName) (Tool, bool)
	Names() []model.ToolName // exactly the five of DR-16
	Dispatch(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error)
}

// Journal is 02's name for what F06 called StepStore. One component, one
// name (DR-15, verbatim).
type Journal interface {
	Create(ctx context.Context, tid model.TenantID, inv model.Investigation) error
	AppendStep(ctx context.Context, tid model.TenantID, invID string, st model.Step) error // fsynced before the loop advances
	AppendEvidence(ctx context.Context, tid model.TenantID, invID string, ev []model.Evidence) error
	Steps(ctx context.Context, tid model.TenantID, invID string) ([]model.Step, error)
	SetStatus(ctx context.Context, tid model.TenantID, invID string, st model.InvestigationStatus, at time.Time) error
	ListRunning(ctx context.Context) ([]model.Investigation, error) // startup reconciliation (04 §S9)
}

// SchemaValidator is DR-15, verbatim.
type SchemaValidator interface {
	ValidateReasonerOutput(raw []byte) (Proposal, error)                        // strict: an unknown field is an error
	ValidateToolArgs(a ToolArgs, inc model.Incident, topo TopologyReader) error // DR-16 §16.3
}

// Sanitizer is DR-15, verbatim.
type Sanitizer interface {
	Wrap(k model.UntrustedKind, s string) string
	Canary() string
	CheckEcho(modelOutput string) error
}

// Budget is DR-15, verbatim. Distinct from model.Budget, the per-investigation
// value-type snapshot this interface charges against.
type Budget interface {
	Charge(ctx context.Context, tid model.TenantID, invID string, d Charge) error
	Remaining(invID string) model.Spend
	Terminated(invID string) (model.TerminationReason, bool)
}

// Charge is Budget.Charge's delta parameter. The register never gives an
// explicit field list here, but DR-17 §17.5's "Docs to change" row states it
// exactly: "03 §3 (Charge(tokensIn, cachedIn, tokensOut, cost))".
type Charge struct {
	TokensIn       int64
	CachedTokensIn int64
	TokensOut      int64
	CostMicroUSD   int64
}

// TopologyReader is DR-15, verbatim: consumer-declared per DR-2's signature
// rule, satisfied structurally by topology.LiveGraph without rca importing
// topology for this interface.
type TopologyReader interface {
	Has(ctx context.Context, tid model.TenantID, service string) bool
}

// Stats is Engine.Stats's return type. The register never gives an explicit
// field list.
// TODO(DR-15): under-specified.
type Stats struct {
	RunningInvestigations int
	QueueDepth            int
}

// --- DR-16: the closed tool set ---

// ToolTraceQuery etc. are the closed five-tool set of DR-16 §16.1: "trace_query,
// log_query, metric_query, topology_query, memory_query. Adding a sixth is an
// architecture change — a new DR — never a config value." Aliased to
// model.ToolName's canonical constants rather than re-declaring the string
// literals.
const (
	ToolTraceQuery    = model.ToolTraceQuery
	ToolLogQuery      = model.ToolLogQuery
	ToolMetricQuery   = model.ToolMetricQuery
	ToolTopologyQuery = model.ToolTopologyQuery
	ToolMemoryQuery   = model.ToolMemoryQuery
)

// ToolArgs is DR-16 §16.2, verbatim. Exactly one pointer field is non-nil,
// and it MUST match Tool. The validator enforces this before dispatch. There
// is no free-form string and no map[string]any anywhere in this type (Params
// on MetricQueryArgs is the one exception, and it is closed by its template).
type ToolArgs struct {
	TenantID model.TenantID // DR-5: mandatory, checked again inside every Tool.Invoke
	Tool     model.ToolName
	Trace    *TraceQueryArgs
	Log      *LogQueryArgs
	Metric   *MetricQueryArgs
	Topology *TopologyQueryArgs
	Memory   *MemoryQueryArgs
}

// TraceQueryArgs is DR-16 §16.2, verbatim.
type TraceQueryArgs struct {
	Service, Operation string
	MinDurationMillis  uint32
	Status             model.StatusFilter // any | ok | error
	AttrEquals         []AttrEqual        // <= 4; every Key MUST be in store.hot.indexed_attribute_keys
	ErrorSigID         string
	PathSignature      uint64
	Start, End         time.Time
	Limit              int      // clamped to rca.budget.max_rows_per_tool_call (500)
	Project            []string // closed field allowlist, <= 12 names
}

// AttrEqual is DR-16 §16.2, verbatim.
type AttrEqual struct {
	Key, Value string // Value <= 128 bytes, matched as a LITERAL, never a pattern
}

// LogQueryArgs is DR-16 §16.2, verbatim.
type LogQueryArgs struct {
	TraceID    model.TraceID // either this ...
	Service    string        // ... or (Service, Start, End)
	Start, End time.Time
	Contains   string // <= 64 bytes; matched as a LITERAL substring SERVER-SIDE after retrieval.
	// It is never interpolated into LogQL, ES DSL or any backend query language.
	Limit int // clamped to 200 lines
}

// MetricQueryArgs is DR-16 §16.2, verbatim.
type MetricQueryArgs struct {
	TemplateID  string            // MUST be a registered template id — free PromQL is not representable
	Params      map[string]string // keys fixed by the template; each value validated by its declared param type
	RED         *REDQuery         // alternative shape: red(service, operation, window)
	Start, End  time.Time
	StepSeconds int // clamped to [10, 300]; points clamped to 1000
}

// REDQuery is DR-16 §16.2, verbatim.
type REDQuery struct {
	Service, Operation string
}

// TopologyQueryArgs is DR-16 §16.2, verbatim.
type TopologyQueryArgs struct {
	Service    string
	Hops       int // 1..3
	Direction  topology.Direction
	Start, End time.Time
	Limit      int // clamped to 500 edges
}

// MemoryQueryArgs is DR-16 §16.2, verbatim.
type MemoryQueryArgs struct {
	Fingerprint string // supplied from the incident by the Engine; the reasoner may NOT author one
	Text        string // <= 256 bytes
	TopK        int    // clamped to memory.retrieval.top_k (8)
}
