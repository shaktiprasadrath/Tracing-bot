package model

import "time"

// RecordKind is 01 §4.6's `memory.Kind`, promoted to model because DR-19's
// memory.Store interface returns model.Record directly (e.g.
// `Similar(...) ([]model.Record, error)`) and DR-4's deletion table names
// "01 §4.6" as the sole canonical location. Renamed from the bare "Kind" to
// avoid colliding with model.AnomalyKind and model.ActionType's own
// closed-enum naming in this package.
type RecordKind uint8

const (
	RecordInvestigation RecordKind = 1
	RecordRunbook       RecordKind = 2
	RecordCorrection    RecordKind = 3
	RecordServiceMeta   RecordKind = 4 // DR-38 §38.1: "memory.Record.Kind = ServiceMeta"
)

// Provenance is DR-19 §19.5's closed enum: every retrieved memory record is
// wrapped by rca.Sanitizer unless ProvHumanCurated.
type Provenance uint8

const (
	ProvHumanCurated Provenance = 1
	ProvLLMAuthored  Provenance = 2
	ProvImported     Provenance = 3
)

// Record is 01 §4.6's memory.Record, promoted to model per DR-19's explicit
// `model.Record` usage throughout memory.Store. Base fields are 01 §4.6
// verbatim (Tenant retyped to TenantID per DR-5, Kind retyped to
// RecordKind); DR-19 §19.5 adds Provenance and TrustTier.
type Record struct {
	ID                    string
	Tenant                TenantID
	Kind                  RecordKind
	Fingerprint           string   // symptom fingerprint, joins to Incident.Fingerprint
	FingerprintTerms      []string // tokenized for the memory_fp_token inverted index (DR-19 §19.2)
	Symptom               string
	RootCause             string
	Resolution            string
	Services              []string
	ErrorSignatures       []string
	SourceInvestigationID string
	SupersedesID          string    // correction chain (DR-19 §19.4)
	Embedding             []float32 // dim 256 hashed by default; provider dim when embeddings.driver != hashed
	EmbeddingModel        string
	Weight                float64 // retrieval weight: +0.15 per confirmation, -0.35 per correction, half-life 90d (DR-19 §19.4)
	Confirmations         int
	Corrections           int

	Provenance Provenance // DR-19 §19.5
	TrustTier  uint8      // 1 human-curated, 2 imported, 3 llm-authored (DR-19 §19.5)

	Tags         []string
	BodyMarkdown string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastUsedAt   time.Time
	TTLDays      int
}

// InvestigationRecord is 01 §4.1's portable DTO written to memory — it
// "breaks the rca <-> memory import cycle" (01's own comment), and DR-2's
// cycle-break table confirms it: "memory.Store.Record takes
// model.InvestigationRecord (CC-1b)"; rca.Investigation.Summary() returns
// one (DR-2's cycle-break table). Tenant retyped to TenantID per DR-5.
type InvestigationRecord struct {
	InvestigationID string
	IncidentID      string
	Tenant          TenantID
	Fingerprint     string
	Symptom         string
	RootCause       string
	Confidence      float64
	Services        []string
	ErrorSignatures []string
	EvidenceRefs    []string
	ReasonerKind    ReasonerKind
	ConcludedAt     time.Time
	BodyMarkdown    string
}
