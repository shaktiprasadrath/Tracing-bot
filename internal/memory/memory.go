package memory

import (
	"context"
	"io"
	"time"

	"traceiq/internal/llm"
	"traceiq/internal/model"
)

// Fingerprint is DR-19 §19.1, verbatim.
type Fingerprint struct {
	TenantID model.TenantID // ALWAYS present, ALWAYS first
	Tokens   []string       // sorted, deduped, <= 24
	Hash     string         // "fp1:" + hex(xxh3( tenantID ‖ "\x00" ‖ join(Tokens, "\x01") ))
}

// Compute is DR-19 §19.1, verbatim signature. Implementation lives in
// fingerprint.go — see that file's doc comment for how the DR-2/DR-14 §14.5
// tension flagged here was resolved.

// SimilarityScorer is DR-19 §19.3, verbatim.
type SimilarityScorer interface {
	Name() string // "lexical" | "hashed" | "anthropic"
	Score(ctx context.Context, tid model.TenantID, q Query, cand []Candidate) ([]Scored, error)
}

// Query is SimilarityScorer.Score's query parameter. The register never
// gives an explicit field list.
// TODO(DR-19): under-specified.
type Query struct {
	Fingerprint Fingerprint
	Text        string
}

// Candidate is SimilarityScorer.Score's candidate parameter. The register
// never gives an explicit field list.
// TODO(DR-19): under-specified.
type Candidate struct {
	Record model.Record
}

// Scored is SimilarityScorer.Score's result element. The register never
// gives an explicit field list.
// TODO(DR-19): under-specified.
type Scored struct {
	Record model.Record
	Score  float64
}

// Embedder is DR-19 §19.3, verbatim.
type Embedder interface {
	Embed(ctx context.Context, tid model.TenantID, texts []string) ([][]float32, error)
	Dim() int
	Name() string
}

// EmbedderLLM is memory's llm.Client-backed Embedder (package doc.go, DR-2's
// cycle-break table: "the embedding LLM call goes through internal/llm.Client
// ... memory.EmbedderLLM and rca.LLMReasoner both hold llm.Client").
// TODO(DR-19/DR-34): under-specified beyond "holds an llm.Client".
type EmbedderLLM struct {
	Client llm.Client
}

// Store is DR-19 §19.3, verbatim.
type Store interface {
	Record(ctx context.Context, tid model.TenantID, rec model.InvestigationRecord) error // DR-2: NOT rca.Investigation
	Similar(ctx context.Context, tid model.TenantID, fp Fingerprint, topK int) ([]model.Record, error)
	Search(ctx context.Context, tid model.TenantID, text string, topK int) ([]model.Record, error)
	Get(ctx context.Context, tid model.TenantID, id string) (model.Record, error)
	Correct(ctx context.Context, tid model.TenantID, c model.Correction) (model.Record, error)
	Confirm(ctx context.Context, tid model.TenantID, id, by string) error
	Delete(ctx context.Context, tid model.TenantID, id, by string) error
	Import(ctx context.Context, tid model.TenantID, r io.Reader, f ExportFormat, by string) (ImportReport, error)
	Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error
	Consolidate(ctx context.Context, now time.Time) (ConsolidateReport, error)
	Stats() Stats
}

// ExportFormat is DR-19 §19.6's closed set: "Export(ctx, tid, w, f) with
// f ∈ {json, markdown}".
type ExportFormat string

const (
	ExportJSON     ExportFormat = "json"
	ExportMarkdown ExportFormat = "markdown"
)

// ImportReport is Store.Import's return type. The register never gives an
// explicit field list.
// TODO(DR-19): under-specified.
type ImportReport struct {
	Imported int
	Skipped  int
	Errors   []string
}

// ConsolidateReport is Store.Consolidate's return type. The register never
// gives an explicit field list.
// TODO(DR-19): under-specified.
type ConsolidateReport struct {
	PairsExamined int64
	Merged        int
	Duration      time.Duration
}

// Stats is Store.Stats's return type. The register never gives an explicit
// field list.
// TODO(DR-19): under-specified.
type Stats struct {
	Records       int64
	VocabularyLen int
}

// RunbookImporter is DR-19 §19.3, verbatim.
type RunbookImporter interface {
	Import(ctx context.Context, tid model.TenantID, markdown []byte, by string) ([]model.Record, error)
}
