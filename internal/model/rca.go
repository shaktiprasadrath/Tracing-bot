package model

import "time"

// Phase is the RCA loop phase (DR-15, matching 01 §4.4's rca.Phase verbatim).
type Phase uint8

const (
	PhaseContextualize Phase = 1
	PhaseHypothesize   Phase = 2
	PhaseTest          Phase = 3
	PhaseValidate      Phase = 4
	PhaseReport        Phase = 5
)

// InvestigationStatus is 01 §4.4's `rca.Status`, promoted to model alongside
// Investigation (DR-15's type-addition rule; renamed from bare "Status" to
// avoid colliding with every other package's own Stats()/status vocabulary).
type InvestigationStatus uint8

const (
	InvestigationRunning         InvestigationStatus = 1
	InvestigationConcluded       InvestigationStatus = 2
	InvestigationInconclusive    InvestigationStatus = 3
	InvestigationBudgetExhausted InvestigationStatus = 4
	InvestigationFailed          InvestigationStatus = 5
	InvestigationAborted         InvestigationStatus = 6
)

// HypothesisCategory is CLOSED (DR-15 §"the type additions to 01 §4.4") —
// this is what makes a hypothesis machine-scorable (CC-18, DR-36 §36.5).
type HypothesisCategory uint8

const (
	CatUnknown            HypothesisCategory = 0
	CatSaturation         HypothesisCategory = 1
	CatDependencyFailure  HypothesisCategory = 2
	CatDeployRegression   HypothesisCategory = 3
	CatConfigChange       HypothesisCategory = 4
	CatResourceExhaustion HypothesisCategory = 5
	CatNetwork            HypothesisCategory = 6
	CatDataSkew           HypothesisCategory = 7
	CatExternalProvider   HypothesisCategory = 8
)

// HypothesisStatus is named in DR-15's Hypothesis field comment ("proposed |
// testing | supported | refuted | inconclusive") but never given an
// explicit const block in the register; the five values are transcribed
// from that prose list.
type HypothesisStatus uint8

const (
	HypothesisProposed     HypothesisStatus = 1
	HypothesisTesting      HypothesisStatus = 2
	HypothesisSupported    HypothesisStatus = 3
	HypothesisRefuted      HypothesisStatus = 4
	HypothesisInconclusive HypothesisStatus = 5
)

// HypothesisSource is named in DR-15's Hypothesis field comment and used in
// DR-19 §19.5 ("Hypothesis.Source = memory"); the register never gives an
// explicit const block. The four values are transcribed from DR-15's
// "detector | memory | rule | llm" list.
type HypothesisSource uint8

const (
	HypSourceDetector HypothesisSource = 1
	HypSourceMemory   HypothesisSource = 2
	HypSourceRule     HypothesisSource = 3
	HypSourceLLM      HypothesisSource = 4
)

// Hypothesis is DR-15's replacement for 01 §4.4's rca.Hypothesis (which had
// Index/TestStepIDs/string-typed Source instead of Category/Component and
// the typed HypothesisSource/HypothesisStatus above). F06's rule catalogue
// and the LLM reasoner both populate Category and Component (DR-15).
type Hypothesis struct {
	ID, Statement         string
	Category              HypothesisCategory
	Component             string // MUST resolve in topology at validation time, or be ""
	PriorScore, PostScore float64
	Status                HypothesisStatus
	Source                HypothesisSource
}

// TerminationReason records which budget dimension ended an investigation
// (DR-15's type additions; consumed by DR-17 §17.2's "reachable step count"
// discussion).
type TerminationReason uint8

const (
	TermConcluded    TerminationReason = 1
	TermSteps        TerminationReason = 2
	TermToolCalls    TerminationReason = 3
	TermTokens       TerminationReason = 4
	TermCachedTokens TerminationReason = 5
	TermCost         TerminationReason = 6
	TermWallClock    TerminationReason = 7
	TermAborted      TerminationReason = 8
	TermFailed       TerminationReason = 9
)

// ReasonerKind distinguishes the LLM reasoner from the deterministic rules
// reasoner (DR-15's rca.Reasoner.Kind(), DR-34 §34.4's mid-loop fallback).
type ReasonerKind string

const (
	ReasonerLLM   ReasonerKind = "llm"
	ReasonerRules ReasonerKind = "rules"
)

// ReasonerSwap records a mid-investigation llm -> rules fallback (DR-15's
// type additions; DR-34 §34.4 gives the exact semantics and the six
// triggers).
type ReasonerSwap struct {
	AtStep   int
	From, To ReasonerKind
	Reason   string
	At       time.Time
}

// ReplayMode selects Engine.Replay's semantics (DR-18 §18.2).
type ReplayMode uint8

const (
	ReplayRecorded ReplayMode = 1 // zero tool calls, zero LLM calls, zero spend
	ReplayLiveDiff ReplayMode = 2 // re-dispatch each tool with the STORED args; diff hashes
)

// ToolName is the closed five-tool set (DR-16 §16.1). "Adding a sixth is an
// architecture change — a new DR — never a config value."
type ToolName string

const (
	ToolTraceQuery    ToolName = "trace_query"
	ToolLogQuery      ToolName = "log_query"
	ToolMetricQuery   ToolName = "metric_query"
	ToolTopologyQuery ToolName = "topology_query"
	ToolMemoryQuery   ToolName = "memory_query"
)

// StatusFilter is rca.TraceQueryArgs.Status's type (DR-16 §16.2: "any | ok | error").
type StatusFilter string

const (
	StatusFilterAny   StatusFilter = "any"
	StatusFilterOK    StatusFilter = "ok"
	StatusFilterError StatusFilter = "error"
)

// StepVerdict is the closed set of tool-call/step outcomes. DR-16 §16.3
// states "Three and only three tool-call outcomes exist: ok, invalid_args,
// unavailable"; DR-34 §34.1 adds "refused" (stop_reason == "refusal");
// DR-37 §37.1(3) adds "schema_error" (a canary violation).
type StepVerdict string

const (
	VerdictOK          StepVerdict = "ok"
	VerdictInvalidArgs StepVerdict = "invalid_args"
	VerdictUnavailable StepVerdict = "unavailable"
	VerdictRefused     StepVerdict = "refused"
	VerdictSchemaError StepVerdict = "schema_error"
)

// Budget is 01 §4.4's rca.Budget value type (the per-investigation budget
// snapshot — distinct from the rca.Budget *interface* DR-15 declares for
// charging/remaining/termination queries). Field set is 01's base plus
// DR-17 §17.1/§17.5's new dimensions (MaxCachedTokensIn, MaxStepWallClock).
type Budget struct {
	WallClock          time.Duration // default 5m
	MaxStepWallClock   time.Duration // DR-17 §17.2, new: per-step ceiling, default 45s
	MaxSteps           int           // default 24
	MaxToolCalls       int           // default 40
	MaxTokensIn        int           // UNCACHED input tokens; default 120000
	MaxCachedTokensIn  int           // DR-17 §17.1, new: cache reads, charged separately; default 600000
	MaxTokensOut       int           // default 64000 (DR-17 §17.1; was 16000)
	MaxCostMicroUSD    int64         // default 500000 ($0.50)
	MaxRowsPerToolCall int           // default 500
	MaxEvidenceBytes   int           // default 8192 (DR-17 §17.1; was 32768)
}

// Spend is DR-17 §17.2's exact carried-fields list: "Spend carries TokensIn,
// CachedTokensIn, TokensOut, CostMicroUSD, ToolCalls, Steps, WallClockMillis."
// This supersedes 01 §4.4's rca.Spend (which had Elapsed time.Duration and no
// CachedTokensIn dimension).
type Spend struct {
	TokensIn        int64
	CachedTokensIn  int64
	TokensOut       int64
	CostMicroUSD    int64
	ToolCalls       int
	Steps           int
	WallClockMillis int64
}

// Investigation is 01 §4.4's rca.Investigation, promoted to model (DR-15's
// type-addition rule applies the same leaf-package reasoning as Event/
// Incident: nl, api, memory and eval all need to read Investigation without
// importing rca). Base fields are 01 §4.4 verbatim (Tenant retyped to
// TenantID per DR-5, ReasonerKind retyped to model.ReasonerKind).
// DR-15 additions: Phase, ReasonerSwaps, TerminationReason, IncidentIDs.
// DR-18 additions: ParentID, ReplayOf, ReplayMode (ReplaySeed/PromptVersion/
// ModelID were already present in 01's base and are not duplicated).
type Investigation struct {
	ID          string
	Tenant      TenantID
	IncidentID  string   // the originating incident
	IncidentIDs []string // DR-15: every incident deduped onto this investigation (DR-17 §17.3), including IncidentID
	Status      InvestigationStatus
	Phase       Phase  // DR-15 addition
	Trigger     string // "auto" | "api" | "chat" | "eval"

	Hypotheses       []Hypothesis
	Steps            []Step
	RootCause        string
	Confidence       float64
	BlastRadius      []string
	SuggestedActions []ActionProposal
	MemoryHits       []string // memory.Record IDs used in Contextualize

	ReasonerKind  ReasonerKind
	ReasonerSwaps []ReasonerSwap // DR-15 addition; DR-34 §34.4 gives the semantics
	ModelID       string         // e.g. "claude-opus-5" (DR-34); empty for rules
	PromptVersion string         // template version, for replay fidelity (DR-18)

	Budget            Budget
	Spend             Spend
	TerminationReason TerminationReason // DR-15 addition

	ReplaySeed int64      // crypto/rand at creation; seeds every tie-break (DR-18 §18.1)
	ParentID   string     // DR-18 addition: set on a replay's result investigation
	ReplayOf   string     // DR-18 addition: original investigation ID, for a replay result
	ReplayMode ReplayMode // DR-18 addition: which mode produced this investigation, if any

	ReportMarkdown string
	Error          string
	StartedAt      time.Time
	EndedAt        time.Time
	Version        int // optimistic concurrency for corrections
}

// Step is DR-18 §18.1's exact persisted shape — the full replacement for 01
// §4.4's rca.Step (which had inline Thought/HypothesisID/EvidenceIDs/
// Corrected fields; DR-18 moves narrative content to store.ObjectStore
// refs+hashes so control.db's write path stays inside DR-6's budget).
// EvidenceIDs is added here even though DR-18's own block omits it, because
// DR-36 §36.5 scores "Investigation.Steps[].EvidenceIDs -> Evidence.Category"
// — without the field DR-36's own formula would not compile. See
// docs/reports/scaffold-report.md.
type Step struct {
	ID, InvestigationID string
	Tenant              TenantID
	Seq                 int
	Phase               Phase
	Tool                ToolName // "" for a pure-reasoning step

	// --- tool inputs, verbatim and re-executable ---
	ToolArgsJSON string // canonical (RFC 8785) JSON of the typed ToolArgs, TenantID elided
	ToolArgsHash string // "sha256:" + hex

	// --- tool result: hash AND ref, never the body inline ---
	ToolResultHash                string // "sha256:" + hex over the canonical result bytes
	ToolResultRef                 string // "evidence/<tenant>/<invID>/<seq>.json.zst" in store.ObjectStore
	ToolResultBytes               int64
	Truncated, Clamped, FromCache bool

	// --- reasoner output, raw ---
	ReasonerOutputRef  string // "reasoner/<tenant>/<invID>/<seq>.json.zst"
	ReasonerOutputHash string
	PromptHash         string // sha256 of the exact rendered prompt

	EvidenceIDs []string // DR-36 §36.5's usage; not in DR-18's own block — see doc comment above

	Verdict                             StepVerdict
	StartedAt, EndedAt                  time.Time
	LatencyMillis                       int64
	TokensIn, CachedTokensIn, TokensOut int64
	CostMicroUSD                        int64
}

// ToolResult is referenced throughout the register as `model.ToolResult`
// (DR-15's Tool.Invoke/ToolRegistry.Dispatch, DR-16 §16.3's "ToolResult.
// Clamped = true", DR-37's "only model.ToolResult projections reach a
// prompt") but, unlike DR-4's other model DTOs, no field list for it
// appears anywhere in the register. Previously declared locally as
// rca.ToolResult (a scope-lock workaround from an earlier pass, per
// docs/reports/w2-scaffold-b.md); moved here now that internal/model is in
// scope, since every reference to it in the register is qualified
// `model.ToolResult`, not `rca.ToolResult`.
//
// w11 (rules-reasoner wave): two fields added, additive only, to close the
// gap the rules reasoner actually hit rather than leaving TODO forever:
//   - Tool: DR-18 §18.1's Step.Tool is "" for a pure-reasoning step but a
//     dispatched result always knows which of the closed five tools produced
//     it; Step derives ToolResultHash/Ref from the SAME ToolResult, so
//     carrying Tool here lets Journal/Replay stamp Step.Tool without
//     threading a second parameter through every Tool.Invoke call site.
//   - ObservedAt: DR-18 §18.2's ReplayLiveDiff drift check ("sha256(newResult)
//     vs ToolResultHash") needs a result-local timestamp distinct from
//     Step.StartedAt/EndedAt (which are about the RPC, not the queried data's
//     recency) so a rule's confirm predicate can reason about staleness
//     without re-deriving it from the Step envelope.
//
// Deliberately NOT added: a typed union of the five tools' row shapes. DR-16
// §16.2's "no free-form string, no map[string]any" rule governs ToolArgs
// (the request), not the response; Rows stays the one escape hatch — a
// canonical JSON projection, already capped and typed at the call site by
// each Tool's own row struct (see internal/rca/tools.go) — because a closed
// union here would need one arm per tool's per-row schema and DR-16 never
// prints that schema. See docs/reports/w11-rca-rules.md.
// TODO(DR-15/DR-16): still under-specified beyond this wave's additions —
// the register never prints its field list.
type ToolResult struct {
	Rows      []byte // canonical JSON projection, capped by rca.budget.max_evidence_bytes
	Clamped   bool
	Truncated bool
	FromCache bool

	Tool       ToolName  // which of the closed five tools produced Rows; "" only for a pure-reasoning step's absent result
	ObservedAt time.Time // freshness of the queried data itself, distinct from the Step RPC's StartedAt/EndedAt
}

// Conclusion is Reasoner.Conclude's return type (DR-15). The register never
// gives an explicit field list for it; fields below are the minimum implied
// by how a conclusion feeds Investigation.RootCause/Confidence/Hypotheses.
// Placeholder — see docs/reports/scaffold-report.md.
type Conclusion struct {
	RootCause  string
	Confidence float64
	Hypotheses []Hypothesis
	Rationale  string // display only, never reaches a tool or the cluster (DR-15's Proposal.Rationale rule applies equally here)
}

// Correction is the parameter type shared by rca.Engine.Correct (a step-
// scoped RCA correction) and memory.Store.Correct (a whole-record
// correction, DR-19 §19.4's -0.35 weight / new Correction-kind record
// semantics). The register describes the correction *arithmetic* in detail
// but never prints a `type Correction struct` — this is a reconstruction.
// See docs/reports/scaffold-report.md.
type Correction struct {
	ID          string
	TargetID    string // a Step.ID (rca) or a Record.ID (memory), depending on caller
	Reason      string
	CorrectedBy string
	CorrectedAt time.Time
	NewValue    string // corrected root cause / resolution text, caller-defined
}
