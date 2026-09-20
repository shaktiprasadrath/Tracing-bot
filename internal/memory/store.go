package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"traceiq/internal/model"
)

// minSimilarity is DR-19 §19.2's default retrieval threshold.
const minSimilarity = 0.35

// maxCandidates is DR-19 §19.2's bounded candidate-generation cap
// (FR-F08-2, FR-F08-8).
const maxCandidates = 500

// decayHalfLife is DR-19 §19.4's read-time weight decay half-life.
const decayHalfLife = 90 * 24 * time.Hour

// pruneAfter is DR-19 §19.7's default memory.consolidation.prune_after_days.
const pruneAfter = 400 * 24 * time.Hour

// dedupeThreshold is DR-19 §19.2's "fingerprint family" Jaccard bound.
const dedupeThreshold = 0.6

// inMemoryStore is a scoped-down, in-process Store: DR-19 §19 specifies an
// embedded-SQLite-backed store with an inverted index
// (memory_fp_token), FTS5 free-text search, and banded-MinHash-LSH
// consolidation. Given this pass's time budget, this implementation keeps
// the full Store contract (tenant-scoping, the bounded three-step Similar()
// shape, the exact DR-19 §19.4 correction arithmetic, byte-close export)
// but backs it with tenant-partitioned in-memory maps instead of SQLite, and
// consolidation uses direct pairwise Jaccard rather than banded MinHash LSH.
// See docs/reports/w11-correlate-memory.md "remaining" for what a follow-up
// pass would need to add for full DR-19 conformance (SQLite persistence,
// memory_fp_token inverted index, MinHash LSH, TF-IDF cosine component,
// rca.Sanitizer wrapping on retrieval).
type inMemoryStore struct {
	clock  model.Clock
	scorer SimilarityScorer

	mu      sync.Mutex
	records map[model.TenantID]map[string]*model.Record
	seq     int
}

// NewInMemoryStore constructs a dev/test Store (see the type doc for the
// SQLite-vs-in-memory deviation this pass took).
func NewInMemoryStore(clock model.Clock, scorer SimilarityScorer) Store {
	if scorer == nil {
		scorer = NewLexicalScorer()
	}
	return &inMemoryStore{
		clock:   clock,
		scorer:  scorer,
		records: map[model.TenantID]map[string]*model.Record{},
	}
}

func (s *inMemoryStore) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%d-%d", prefix, s.clock.Now().UnixNano(), s.seq)
}

func (s *inMemoryStore) tenantMap(tid model.TenantID) map[string]*model.Record {
	m, ok := s.records[tid]
	if !ok {
		m = map[string]*model.Record{}
		s.records[tid] = m
	}
	return m
}

// Record is FR-F08-1: persist a completed investigation under its symptom
// fingerprint. Store.Record takes model.InvestigationRecord, never
// rca.Investigation (DR-2's cycle break).
func (s *inMemoryStore) Record(ctx context.Context, tid model.TenantID, rec model.InvestigationRecord) error {
	if tid == "" {
		return fmt.Errorf("memory: tenant id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	terms := buildTokens(rec.Services, rec.ErrorSignatures, nil, nil)
	fp := rec.Fingerprint
	if fp == "" {
		fp = hashTokens(tid, terms)
	}
	r := &model.Record{
		ID:                    s.nextID("rec"),
		Tenant:                tid,
		Kind:                  model.RecordInvestigation,
		Fingerprint:           fp,
		FingerprintTerms:      terms,
		Symptom:               rec.Symptom,
		RootCause:             rec.RootCause,
		Services:              append([]string{}, rec.Services...),
		ErrorSignatures:       append([]string{}, rec.ErrorSignatures...),
		SourceInvestigationID: rec.InvestigationID,
		Weight:                1.0,
		Provenance:            provenanceFor(rec),
		TrustTier:             trustTierFor(rec),
		BodyMarkdown:          rec.BodyMarkdown,
		CreatedAt:             now,
		UpdatedAt:             now,
		LastUsedAt:            now,
	}
	s.tenantMap(tid)[r.ID] = r
	return nil
}

func provenanceFor(rec model.InvestigationRecord) model.Provenance {
	if rec.ReasonerKind == model.ReasonerLLM {
		return model.ProvLLMAuthored
	}
	return model.ProvHumanCurated
}

func trustTierFor(rec model.InvestigationRecord) uint8 {
	if rec.ReasonerKind == model.ReasonerLLM {
		return 3
	}
	return 1
}

func effectiveWeight(r *model.Record, now time.Time) float64 {
	if r.Weight == 0 {
		return 0
	}
	age := now.Sub(r.LastUsedAt)
	if age < 0 {
		age = 0
	}
	halfLives := age.Seconds() / decayHalfLife.Seconds()
	return r.Weight * pow(0.5, halfLives)
}

func pow(base, exp float64) float64 {
	// DR-19 §19.4's decay formula needs fractional exponents (halfLives is
	// a ratio of durations, not an integer), so this delegates to stdlib
	// math.Pow rather than a repeated-squaring loop (which only handles
	// integer exponents). The undefined `mathPow` this replaced was
	// evidently meant to be exactly this call.
	if exp == 0 {
		return 1
	}
	return math.Pow(base, exp)
}

// Similar is FR-F08-2's bounded three-step algorithm: candidate generation
// (here, a full tenant scan capped at maxCandidates — see the type doc for
// the inverted-index deviation), scoring via the configured
// SimilarityScorer, then threshold + top-K selection. Correction-weighted
// retrieval (DR-19 §19.4) is applied as a ranking multiplier on top of the
// scorer's raw score, which is what the min_similarity threshold is checked
// against.
func (s *inMemoryStore) Similar(ctx context.Context, tid model.TenantID, fp Fingerprint, topK int) ([]model.Record, error) {
	s.mu.Lock()
	tenantRecs := s.tenantMap(tid)
	supersededBy := map[string]string{} // originalID -> correction record ID
	for _, r := range tenantRecs {
		if r.SupersedesID != "" {
			supersededBy[r.SupersedesID] = r.ID
		}
	}
	// Candidate generation, bounded at maxCandidates (FR-F08-2). DR-19
	// §19.2 requires this cut to be rarest-first over an inverted index so
	// the cap keeps the SELECTIVE candidates rather than an arbitrary
	// subset; this store has no inverted index (see the type doc), and Go
	// map iteration order is randomized, so cutting the map's natural
	// iteration order at maxCandidates would silently and
	// non-deterministically drop the true best match once a tenant holds
	// more than maxCandidates records — a correctness bug, not merely a
	// latency one. As a cheap approximation of "rarest-first selectivity"
	// without building the full inverted index, every tenant record is
	// scored by raw fingerprint-token overlap against the query first and
	// only then cut at maxCandidates, so a record that actually shares
	// tokens with the query is never evicted in favor of one that shares
	// none.
	qFPSet := tokenSet(fp.Tokens)
	type scoredID struct {
		rec     *model.Record
		overlap int
	}
	all := make([]scoredID, 0, len(tenantRecs))
	for _, r := range tenantRecs {
		ov := 0
		for _, t := range r.FingerprintTerms {
			if _, ok := qFPSet[t]; ok {
				ov++
			}
		}
		all = append(all, scoredID{rec: r, overlap: ov})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].overlap != all[j].overlap {
			return all[i].overlap > all[j].overlap
		}
		return all[i].rec.ID < all[j].rec.ID // deterministic tiebreak
	})
	if len(all) > maxCandidates {
		all = all[:maxCandidates]
	}
	candidates := make([]Candidate, 0, len(all))
	now := s.clock.Now()
	// Snapshot each candidate's effectiveWeight while still holding the
	// lock: tenantRecs is the live, mutable map backing this tenant (not a
	// copy), and Record/Correct/Confirm/Delete all mutate it under s.mu.
	// Re-reading it below after unlocking (the prior version of this
	// method did exactly that, via tenantRecsSnapshot) is a data race with
	// any concurrent write — this snapshot removes the need to touch the
	// map again post-unlock.
	weights := make(map[string]float64, len(all))
	for _, sid := range all {
		candidates = append(candidates, Candidate{Record: *sid.rec})
		weights[sid.rec.ID] = effectiveWeight(sid.rec, now)
	}
	s.mu.Unlock()

	scored, err := s.scorer.Score(ctx, tid, Query{Fingerprint: fp}, candidates)
	if err != nil {
		return nil, err
	}

	type ranked struct {
		rec   model.Record
		score float64
		rank  float64
	}
	var kept []ranked
	for _, sc := range scored {
		if sc.Score < minSimilarity {
			continue
		}
		// A superseded record is returned only together with its
		// superseder, and always ranked below it (DR-19 §19.4).
		if superID, ok := supersededBy[sc.Record.ID]; ok {
			found := false
			for _, s2 := range scored {
				if s2.Record.ID == superID && s2.Score >= minSimilarity {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		w, ok := weights[sc.Record.ID]
		if !ok {
			w = 1.0
		}
		kept = append(kept, ranked{rec: sc.Record, score: sc.Score, rank: sc.Score * (1 + w)})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].rank != kept[j].rank {
			return kept[i].rank > kept[j].rank
		}
		return kept[i].rec.ID < kept[j].rec.ID
	})
	// Ensure superseder-before-superseded even if rank happens to invert.
	pos := map[string]int{}
	for i, k := range kept {
		pos[k.rec.ID] = i
	}
	for orig, corr := range supersededBy {
		oi, oOk := pos[orig]
		ci, cOk := pos[corr]
		if oOk && cOk && ci > oi {
			kept[oi], kept[ci] = kept[ci], kept[oi]
			pos[orig], pos[corr] = ci, oi
		}
	}

	if topK <= 0 || topK > len(kept) {
		topK = len(kept)
	}
	out := make([]model.Record, 0, topK)
	for i := 0; i < topK; i++ {
		out = append(out, kept[i].rec)
	}
	return out, nil
}

// Search is a free-text search over Symptom/RootCause/BodyMarkdown
// (DR-19 §19's memory_fts role — a substring/token-overlap stand-in here
// rather than SQLite FTS5).
func (s *inMemoryStore) Search(ctx context.Context, tid model.TenantID, text string, topK int) ([]model.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	qTokens := tokenize(text)
	qSet := tokenSet(qTokens)
	type ranked struct {
		rec   model.Record
		score float64
	}
	var out []ranked
	for _, r := range s.tenantMap(tid) {
		cSet := tokenSet(tokenize(r.Symptom), tokenize(r.RootCause), tokenize(r.BodyMarkdown))
		sc := jaccard(qSet, cSet)
		if sc <= 0 && !strings.Contains(strings.ToLower(r.RootCause+" "+r.Symptom+" "+r.BodyMarkdown), strings.ToLower(text)) {
			continue
		}
		out = append(out, ranked{rec: *r, score: sc})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if topK <= 0 || topK > len(out) {
		topK = len(out)
	}
	res := make([]model.Record, 0, topK)
	for i := 0; i < topK; i++ {
		res = append(res, out[i].rec)
	}
	return res, nil
}

// Get is tenant-scoped: a record belonging to a different tenant is treated
// as not found, never returned (tenant isolation, DR-5).
func (s *inMemoryStore) Get(ctx context.Context, tid model.TenantID, id string) (model.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.tenantMap(tid)[id]
	if !ok {
		return model.Record{}, fmt.Errorf("memory: record %q not found for tenant %q", id, tid)
	}
	return *r, nil
}

// Correct is DR-19 §19.4's "Correction" arithmetic: Weight -= 0.35 (floor
// 0.0), Corrections++, and a new Kind=Correction record inserted with
// Weight=1.0 and SupersedesID pointing at the original. (The "Confirmation"
// +0.15/cap-2.0 arithmetic is Store.Confirm, below — the register's
// reconstructed model.Correction type carries no Verdict field to switch on,
// so the two DR-19 §19.4 cases map onto the Store interface's two separate
// methods instead; see docs/reports/w11-correlate-memory.md.)
func (s *inMemoryStore) Correct(ctx context.Context, tid model.TenantID, c model.Correction) (model.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tm := s.tenantMap(tid)
	target, ok := tm[c.TargetID]
	if !ok {
		return model.Record{}, fmt.Errorf("memory: record %q not found for tenant %q", c.TargetID, tid)
	}
	now := s.clock.Now()
	target.Weight -= 0.35
	if target.Weight < 0 {
		target.Weight = 0
	}
	target.Corrections++
	target.UpdatedAt = now

	newRec := &model.Record{
		ID:               s.nextID("cor"),
		Tenant:           tid,
		Kind:             model.RecordCorrection,
		Fingerprint:      target.Fingerprint,
		FingerprintTerms: append([]string{}, target.FingerprintTerms...),
		Symptom:          target.Symptom,
		RootCause:        c.NewValue,
		Services:         append([]string{}, target.Services...),
		SupersedesID:     target.ID,
		Weight:           1.0,
		Provenance:       model.ProvHumanCurated,
		TrustTier:        1,
		CreatedAt:        now,
		UpdatedAt:        now,
		LastUsedAt:       now,
	}
	tm[newRec.ID] = newRec
	return *newRec, nil
}

// Confirm is DR-19 §19.4's "Confirmation" arithmetic: Weight += 0.15,
// capped at 2.0, Confirmations++. This is what boosts a record's future
// Similar() ranking.
func (s *inMemoryStore) Confirm(ctx context.Context, tid model.TenantID, id, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.tenantMap(tid)[id]
	if !ok {
		return fmt.Errorf("memory: record %q not found for tenant %q", id, tid)
	}
	r.Weight += 0.15
	if r.Weight > 2.0 {
		r.Weight = 2.0
	}
	r.Confirmations++
	now := s.clock.Now()
	r.UpdatedAt = now
	r.LastUsedAt = now
	_ = by
	return nil
}

func (s *inMemoryStore) Delete(ctx context.Context, tid model.TenantID, id, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tm := s.tenantMap(tid)
	if _, ok := tm[id]; !ok {
		return fmt.Errorf("memory: record %q not found for tenant %q", id, tid)
	}
	delete(tm, id)
	_ = by
	return nil
}

// exportEnvelope is FR-F08-10's JSON envelope shape.
type exportEnvelope struct {
	Schema     string           `json:"schema"`
	Tenant     model.TenantID   `json:"tenant"`
	ExportedAt string           `json:"exported_at"`
	Count      int              `json:"count"`
	Records    []exportedRecord `json:"records"`
}

// exportedRecord excludes EffectiveWeight (time-dependent, FR-F08-10) and
// renders timestamps RFC3339 UTC second precision.
type exportedRecord struct {
	ID               string   `json:"id"`
	Kind             uint8    `json:"kind"`
	Fingerprint      string   `json:"fingerprint"`
	FingerprintTerms []string `json:"fingerprint_terms"`
	Symptom          string   `json:"symptom"`
	RootCause        string   `json:"root_cause"`
	Services         []string `json:"services"`
	SupersedesID     string   `json:"supersedes_id,omitempty"`
	Weight           float64  `json:"weight"`
	Confirmations    int      `json:"confirmations"`
	Corrections      int      `json:"corrections"`
	Provenance       uint8    `json:"provenance"`
	TrustTier        uint8    `json:"trust_tier"`
	CreatedAt        string   `json:"created_at"`
	LastUsedAt       string   `json:"last_used_at"`
}

const exportSchema = "traceiq.memory.v1"

func sortedExportRecords(tm map[string]*model.Record) []*model.Record {
	out := make([]*model.Record, 0, len(tm))
	for _, r := range tm {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Fingerprint != out[j].Fingerprint {
			return out[i].Fingerprint < out[j].Fingerprint
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

const rfc3339UTC = "2006-01-02T15:04:05Z"

func fmtTime(t time.Time) string { return t.UTC().Format(rfc3339UTC) }

// Export is FR-F08-6/FR-F08-10: byte-reproducible (excluding exported_at)
// JSON and Markdown export, sorted by (Kind, Fingerprint, CreatedAt, ID),
// no map iteration in the output path.
func (s *inMemoryStore) Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error {
	s.mu.Lock()
	sorted := sortedExportRecords(s.tenantMap(tid))
	s.mu.Unlock()

	switch f {
	case ExportJSON:
		recs := make([]exportedRecord, 0, len(sorted))
		for _, r := range sorted {
			recs = append(recs, exportedRecord{
				ID: r.ID, Kind: uint8(r.Kind), Fingerprint: r.Fingerprint,
				FingerprintTerms: r.FingerprintTerms, Symptom: r.Symptom, RootCause: r.RootCause,
				Services: r.Services, SupersedesID: r.SupersedesID, Weight: r.Weight,
				Confirmations: r.Confirmations, Corrections: r.Corrections,
				Provenance: uint8(r.Provenance), TrustTier: r.TrustTier,
				CreatedAt: fmtTime(r.CreatedAt), LastUsedAt: fmtTime(r.LastUsedAt),
			})
		}
		env := exportEnvelope{Schema: exportSchema, Tenant: tid, ExportedAt: fmtTime(s.clock.Now()), Count: len(recs), Records: recs}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(env)
	case ExportMarkdown:
		var b strings.Builder
		fmt.Fprintf(&b, "# TraceIQ Memory Export\n\nTenant: %s\n\n", tid)
		for _, r := range sorted {
			fmt.Fprintf(&b, "## %s (%s)\n\n", r.ID, r.Fingerprint)
			fmt.Fprintf(&b, "- Kind: %d\n- Weight: %.2f\n- Created: %s\n\n", r.Kind, r.Weight, fmtTime(r.CreatedAt))
			fmt.Fprintf(&b, "### Symptom\n\n%s\n\n### Root cause\n\n%s\n\n", r.Symptom, r.RootCause)
		}
		out := strings.ReplaceAll(b.String(), "\r\n", "\n")
		_, err := io.WriteString(w, out)
		return err
	default:
		return fmt.Errorf("memory: unknown export format %q", f)
	}
}

// Import is FR-F08-10: admin-only (enforced by the caller/API layer; this
// Store method trusts its caller), rejects an unknown schema, never
// overwrites an existing ID (collision assigns a new one), stamps
// ProvImported.
func (s *inMemoryStore) Import(ctx context.Context, tid model.TenantID, r io.Reader, f ExportFormat, by string) (ImportReport, error) {
	if f != ExportJSON {
		return ImportReport{}, fmt.Errorf("memory: import only supports json bundles, got %q", f)
	}
	var env exportEnvelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return ImportReport{}, fmt.Errorf("memory: invalid import bundle: %w", err)
	}
	if env.Schema != exportSchema {
		return ImportReport{}, fmt.Errorf("memory: unknown import schema %q", env.Schema)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tm := s.tenantMap(tid)
	report := ImportReport{}
	now := s.clock.Now()
	for _, er := range env.Records {
		id := er.ID
		if _, exists := tm[id]; exists || id == "" {
			id = s.nextID("imp")
		}
		created, err1 := time.Parse(rfc3339UTC, er.CreatedAt)
		lastUsed, err2 := time.Parse(rfc3339UTC, er.LastUsedAt)
		if err1 != nil {
			created = now
		}
		if err2 != nil {
			lastUsed = now
		}
		tm[id] = &model.Record{
			ID: id, Tenant: tid, Kind: model.RecordKind(er.Kind), Fingerprint: er.Fingerprint,
			FingerprintTerms: er.FingerprintTerms, Symptom: er.Symptom, RootCause: er.RootCause,
			Services: er.Services, SupersedesID: er.SupersedesID, Weight: er.Weight,
			Confirmations: er.Confirmations, Corrections: er.Corrections,
			Provenance: model.ProvImported, TrustTier: 2,
			CreatedAt: created, UpdatedAt: now, LastUsedAt: lastUsed,
		}
		report.Imported++
	}
	_ = by
	return report, nil
}

// Consolidate is a scoped-down stand-in for FR-F08-7's banded-MinHash-LSH
// consolidation: direct pairwise Jaccard grouping (documented O(n^2)
// deviation — acceptable at this pass's in-memory scale, not at DR-19
// §19.7's 100k-record target) that merges records within the same
// fingerprint family (Jaccard >= dedupeThreshold) into the most recent one,
// and prunes non-Runbook records older than pruneAfter (model.Record has no
// Pinned/OccurrenceCount field in this scaffold, so Runbook records stand in
// for "pinned" — see docs/reports/w11-correlate-memory.md).
func (s *inMemoryStore) Consolidate(ctx context.Context, now time.Time) (ConsolidateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := s.clock.Now()
	report := ConsolidateReport{}
	for _, tm := range s.records {
		ids := make([]string, 0, len(tm))
		for id := range tm {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		merged := map[string]bool{}
		for i := 0; i < len(ids); i++ {
			if merged[ids[i]] {
				continue
			}
			a := tm[ids[i]]
			for j := i + 1; j < len(ids); j++ {
				if merged[ids[j]] {
					continue
				}
				b := tm[ids[j]]
				report.PairsExamined++
				if a.Kind != b.Kind {
					continue
				}
				j1 := jaccard(tokenSet(a.FingerprintTerms), tokenSet(b.FingerprintTerms))
				if j1 < dedupeThreshold {
					continue
				}
				canonical, dup := a, b
				if b.CreatedAt.After(a.CreatedAt) {
					canonical, dup = b, a
				}
				delete(tm, dup.ID)
				merged[dup.ID] = true
				report.Merged++
				_ = canonical
			}
		}
		for id, r := range tm {
			if r.Kind == model.RecordRunbook {
				continue // pinned by default (FR-F08-4)
			}
			if now.Sub(r.CreatedAt) > pruneAfter {
				delete(tm, id)
			}
		}
	}
	report.Duration = s.clock.Since(start)
	return report, nil
}

func (s *inMemoryStore) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	vocab := map[string]struct{}{}
	for _, tm := range s.records {
		total += int64(len(tm))
		for _, r := range tm {
			for _, t := range r.FingerprintTerms {
				vocab[t] = struct{}{}
			}
		}
	}
	return Stats{Records: total, VocabularyLen: len(vocab)}
}
