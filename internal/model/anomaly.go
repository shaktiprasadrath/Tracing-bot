package model

import "time"

// AnomalyKind mirrors internal/anomaly's Detector.Kind() values (DR-14
// §14.1's `anomaly.Kind`). It is redeclared here, rather than referenced as
// anomaly.Kind, because internal/model is a stdlib-only leaf package (DR-4)
// while internal/anomaly imports internal/model (DR-2) — model.AnomalyEvent
// cannot hold an anomaly.Kind field without creating an import cycle. See
// docs/reports/scaffold-report.md.
type AnomalyKind uint8

const (
	AnomalyLatencyShift      AnomalyKind = 1
	AnomalyErrorBurst        AnomalyKind = 2
	AnomalyNewErrorSignature AnomalyKind = 3
	AnomalyThroughputDrop    AnomalyKind = 4
	AnomalyTopologyChange    AnomalyKind = 5
)

// Severity is the incident severity scale (DR-14 §14.5's score table). Value
// 1 ("Info" in 01 §4.3's original enum) is reserved/unused by that table —
// a score below 0.55 is not an incident at all — but is kept in the const
// block for positional compatibility with 01 §4.3.
type Severity uint8

const (
	SeverityUnknown  Severity = 0
	SeverityInfo     Severity = 1 // reserved; DR-14 §14.5 never produces this value
	SeverityLow      Severity = 2
	SeverityMedium   Severity = 3
	SeverityHigh     Severity = 4
	SeverityCritical Severity = 5
)

// IncidentStatus is 01 §4.3's `anomaly.IncidentStatus`, promoted to model by
// DR-4's deletion table alongside Event/Incident. DR-14 §14.7 additionally
// requires an Expired state (force-close over anomaly.grouping.max_open_incidents),
// which 01's six values already include as Resolved-adjacent — the register
// does not renumber this enum, so it is transcribed verbatim from 01 §4.3.
type IncidentStatus uint8

const (
	IncidentCandidate     IncidentStatus = 1
	IncidentInvestigating IncidentStatus = 2
	IncidentReported      IncidentStatus = 3
	IncidentPaging        IncidentStatus = 4
	IncidentResolved      IncidentStatus = 5
	IncidentSuppressed    IncidentStatus = 6
	IncidentExpired       IncidentStatus = 7 // DR-14 §14.7, added past 01's six values
)

// AnomalyEvent is anomaly.Event promoted to model per DR-4's deletion table
// ("anomaly.Event / anomaly.Incident -> 01 §4.3"). Base field set is 01
// §4.3 verbatim (Tenant retyped to TenantID per DR-5; Kind retyped to
// AnomalyKind per the note above); DR-14 §14.6 changes DeployMarkerID
// (singular) to DeployMarkerIDs (plural, "Event.DeployMarkerIDs" — an event
// may be tagged by more than one nearby deploy) and DR-14 §14.3 adds
// Provisional.
type AnomalyEvent struct {
	ID          string // ULID, time-sortable
	Tenant      TenantID
	Kind        AnomalyKind
	DetectorID  string // e.g. "latency_shift/v1"
	Service     string
	Operation   string
	EdgeID      string // set when Kind == AnomalyTopologyChange
	WindowStart time.Time
	WindowEnd   time.Time
	Observed    float64
	Baseline    float64
	Deviation   float64 // ratio or absolute delta, detector-defined (DR-14 §14.4)
	Score       float64 // clamp01(...), DR-14 §14.4
	Severity    Severity
	ErrorSigID  string

	ExemplarTraceIDs []TraceID // <= 4
	DeployMarkerIDs  []string  // DR-14 §14.6: deploy-window enrichment tags the event in place
	Provisional      bool      // DR-14 §14.3 cold-start state; caps downstream Incident.Score at 0.69

	Explanation string // deterministic, human-readable, never LLM-written
	CreatedAt   time.Time
}

// Incident is anomaly.Incident promoted to model per DR-4. Base field set is
// 01 §4.3 verbatim (Tenant retyped to TenantID per DR-5); Score/Fingerprint/
// EpicenterService/BlastRadius formulas are DR-14 §14.5; Provisional is
// DR-14 §14.3.
type Incident struct {
	ID          string
	Tenant      TenantID
	Title       string // deterministic template, not LLM-written
	Status      IncidentStatus
	Severity    Severity
	Score       float64
	Fingerprint string // "fp1:" + hex(xxh3(...)); identical construction to memory.Fingerprint.Compute (DR-19 §19.1)

	EventIDs         []string
	Services         []string
	EpicenterService string
	BlastRadius      []string
	ExemplarTraceIDs []TraceID
	DeployMarkerIDs  []string

	InvestigationID string
	SuppressedBy    string // dedupe key of the incident/investigation that absorbed this one (DR-14 §14.7, DR-17 §17.3)
	Provisional     bool   // DR-14 §14.3: never reaches Critical, never satisfies DR-21's P3

	FirstSeen time.Time
	LastSeen  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SLOObjective closes DR-21 §21.2's CriticalSLOBreach.
type SLOObjective uint8

const (
	SLOAvailability SLOObjective = 1
	SLOLatency      SLOObjective = 2
)

// CriticalSLOBreach is DR-21 §21.2, verbatim: the only paging bypass (P3),
// explicitly configured and empty by default.
type CriticalSLOBreach struct {
	ID          string // required, unique per tenant; appears verbatim in the page
	Service     string // must resolve in topology at evaluation time, else the rule is inert + a WARN metric
	Objective   SLOObjective
	Threshold   float64       // availability: error ratio 0..1 ; latency: p99 milliseconds
	Window      time.Duration // 1m..60m
	MinDuration time.Duration // sustained for at least this long; default 2m
}

// EvidenceCategory is the closed enum DR-36 §36.4 declares (as
// `eval.EvidenceCategory`) so that EvidencePrecision/EvidenceRecall are
// computable. It is defined once, here, rather than duplicated in
// internal/eval: model.Evidence.Category (below) and
// eval.Expectation.ExpectedEvidence both need the identical closed set, and
// internal/eval already imports internal/model (DR-2), so there is no reason
// for a second declaration. See docs/reports/scaffold-report.md.
type EvidenceCategory uint8

const (
	EvTraceExemplar  EvidenceCategory = 1
	EvErrorSignature EvidenceCategory = 2
	EvLogLine        EvidenceCategory = 3
	EvMetricSeries   EvidenceCategory = 4
	EvTopologyEdge   EvidenceCategory = 5
	EvDeployMarker   EvidenceCategory = 6
	EvREDSeries      EvidenceCategory = 7
	EvMemoryRecord   EvidenceCategory = 8
)

// Evidence is 01 §4.4's `rca.Evidence`, promoted to model alongside
// Investigation/Step (DR-15's type-addition rule applies the same
// leaf-package reasoning: Investigation/Step/Evidence must be constructible
// and readable by rca, nl, memory and api without a cycle). Kind is retyped
// to the closed EvidenceCategory (DR-36 §36.5's scoring requirement) in
// place of 01's free-form EvidenceKind uint8 enum, since DR-36 needs the
// identical closed set on both sides of the eval comparison.
type Evidence struct {
	ID              string
	InvestigationID string
	StepID          string
	Category        EvidenceCategory
	Source          string // "store/sqlite" | "store/parquet" | "loki" | "prometheus" | "memory" | "topology"
	Query           string // exact executed query, replayable by a human
	TraceID         TraceID
	SpanID          SpanID
	Ref             string  // deep link, e.g. /ui/trace/<id>?span=<id>
	Summary         string  // <= 512 bytes, deterministic rendering
	PayloadJSON     []byte  // capped at rca.budget.max_evidence_bytes
	Weight          float64 // contribution to confidence
	Redacted        bool    // true if the 01 §8.3 secret scrubber removed fields
	ObservedAt      time.Time
}
