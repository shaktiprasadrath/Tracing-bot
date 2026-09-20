package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"traceiq/internal/model"
)

// fakeClock is a minimal, local model.Clock stand-in (mirrors
// internal/correlate/correlate_test.go's fakeClock — memory is not
// integration-testing eval.VirtualClock this pass, so it is hand-rolled
// rather than reused across packages).
type fakeClock struct {
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time                                   { return c.now }
func (c *fakeClock) Since(t time.Time) time.Duration                  { return c.now.Sub(t) }
func (c *fakeClock) Advance(d time.Duration)                          { c.now = c.now.Add(d) }
func (c *fakeClock) NewTicker(d time.Duration) model.Ticker           { panic("not used") }
func (c *fakeClock) NewTimer(d time.Duration) model.Timer             { panic("not used") }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

const (
	tenantA model.TenantID = "tenant-a"
	tenantB model.TenantID = "tenant-b"
)

func mustClock() *fakeClock {
	return newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}

// ---------------------------------------------------------------------
// AC-F08-1 / FR-F08-1: fingerprint determinism, tenant scoping
// ---------------------------------------------------------------------

func TestComputeFingerprint_DeterministicUnderReordering(t *testing.T) {
	inc1 := model.Incident{
		Services:        []string{"checkout", "payments"},
		DeployMarkerIDs: []string{"dep-1"},
	}
	inc2 := model.Incident{
		Services:        []string{"payments", "checkout"}, // reordered
		DeployMarkerIDs: []string{"dep-1"},
	}
	fp1 := Compute(tenantA, inc1)
	fp2 := Compute(tenantA, inc2)

	if fp1.Hash != fp2.Hash {
		t.Fatalf("fingerprint hash not order-independent: %q vs %q", fp1.Hash, fp2.Hash)
	}
	if !strings.HasPrefix(fp1.Hash, "fp1:") {
		t.Fatalf("fingerprint hash missing fp1: prefix: %q", fp1.Hash)
	}
	// Tokens themselves must also be sorted/deduped.
	if !sortedNoDupes(fp1.Tokens) {
		t.Fatalf("fingerprint tokens not sorted/deduped: %v", fp1.Tokens)
	}
}

func TestComputeFingerprint_TenantScoped_NeverCollidesAcrossTenants(t *testing.T) {
	inc := model.Incident{Services: []string{"checkout"}}
	fpA := Compute(tenantA, inc)
	fpB := Compute(tenantB, inc)

	if fpA.Hash == fpB.Hash {
		t.Fatalf("two different tenants over identical tokens produced the same fingerprint hash: %q", fpA.Hash)
	}
	// Same token set, different hash: confirms tenant is inside the
	// hash preimage, not merely a filter applied after the fact.
	if len(fpA.Tokens) != len(fpB.Tokens) {
		t.Fatalf("token sets should be identical across tenants for the same incident: %v vs %v", fpA.Tokens, fpB.Tokens)
	}
}

func TestComputeFingerprint_CappedAtMaxTokens(t *testing.T) {
	var services []string
	for i := 0; i < maxFingerprintTokens+10; i++ {
		services = append(services, string(rune('a'+i%26))+string(rune('0'+i%10))+"-svc")
	}
	fp := Compute(tenantA, model.Incident{Services: services})
	if len(fp.Tokens) > maxFingerprintTokens {
		t.Fatalf("fingerprint tokens exceeded cap: got %d, want <= %d", len(fp.Tokens), maxFingerprintTokens)
	}
}

func sortedNoDupes(ts []string) bool {
	seen := map[string]bool{}
	for i, tok := range ts {
		if seen[tok] {
			return false
		}
		seen[tok] = true
		if i > 0 && ts[i-1] > tok {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------
// AC-F08-2: SimilarityScorer ranks a closer match above a distant one
// ---------------------------------------------------------------------

func TestLexicalScorer_RanksCloserMatchHigher(t *testing.T) {
	scorer := NewLexicalScorer()
	q := Query{
		Fingerprint: Fingerprint{Tokens: []string{"svc:checkout", "svc:payments", "errsig:timeout"}},
	}
	near := Candidate{Record: model.Record{
		ID:               "near",
		FingerprintTerms: []string{"svc:checkout", "svc:payments", "errsig:timeout"},
	}}
	far := Candidate{Record: model.Record{
		ID:               "far",
		FingerprintTerms: []string{"svc:inventory"},
	}}

	scored, err := scorer.Score(context.Background(), tenantA, q, []Candidate{far, near})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	var nearScore, farScore float64
	for _, s := range scored {
		if s.Record.ID == "near" {
			nearScore = s.Score
		}
		if s.Record.ID == "far" {
			farScore = s.Score
		}
	}
	if nearScore <= farScore {
		t.Fatalf("expected near match to score higher than far match: near=%v far=%v", nearScore, farScore)
	}
	if nearScore != 1.0 {
		t.Fatalf("expected exact token-set match to score 1.0 (Jaccard), got %v", nearScore)
	}
}

// ---------------------------------------------------------------------
// AC-F08-2/AC-F08-1: Store.Record + Store.Similar round trip
// ---------------------------------------------------------------------

func TestStore_RecordAndSimilar_RoundTrip(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	rec := model.InvestigationRecord{
		InvestigationID: "inv-1",
		Tenant:          tenantA,
		Symptom:         "checkout latency spike",
		RootCause:       "payments service connection pool exhaustion",
		Services:        []string{"checkout", "payments"},
		ErrorSignatures: []string{"conn-pool-exhausted"},
		ReasonerKind:    model.ReasonerRules,
	}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Unrelated record, should not match.
	unrelated := model.InvestigationRecord{
		InvestigationID: "inv-2",
		Tenant:          tenantA,
		Symptom:         "inventory sync lag",
		RootCause:       "kafka consumer backlog",
		Services:        []string{"inventory"},
		ErrorSignatures: []string{"consumer-lag"},
		ReasonerKind:    model.ReasonerRules,
	}
	if err := s.Record(ctx, tenantA, unrelated); err != nil {
		t.Fatalf("Record (unrelated): %v", err)
	}

	fp := fromTokens(tenantA, buildTokens([]string{"checkout", "payments"}, []string{"conn-pool-exhausted"}, nil, nil))
	got, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil {
		t.Fatalf("Similar: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least one similar record, got none")
	}
	if got[0].Symptom != "checkout latency spike" {
		t.Fatalf("expected the matching record ranked first, got %q", got[0].Symptom)
	}
	for _, r := range got {
		if r.Symptom == "inventory sync lag" {
			t.Fatalf("unrelated record should not appear above min_similarity: %+v", r)
		}
	}

	// AC-F08-1: provenance/trust tier for a rules-authored record.
	if got[0].Provenance != model.ProvHumanCurated {
		t.Fatalf("rules-reasoner record should be ProvHumanCurated, got %v", got[0].Provenance)
	}
	if got[0].TrustTier != 1 {
		t.Fatalf("rules-reasoner record should be TrustTier 1, got %v", got[0].TrustTier)
	}
}

func TestStore_Record_LLMReasoner_ProvenanceAndTrustTier(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	rec := model.InvestigationRecord{
		InvestigationID: "inv-llm",
		Tenant:          tenantA,
		Symptom:         "cache stampede",
		RootCause:       "cache miss storm",
		Services:        []string{"cache"},
		ReasonerKind:    model.ReasonerLLM,
	}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	fp := fromTokens(tenantA, buildTokens([]string{"cache"}, nil, nil, nil))
	got, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil {
		t.Fatalf("Similar: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 result, got %d", len(got))
	}
	if got[0].Provenance != model.ProvLLMAuthored {
		t.Fatalf("llm-reasoner record should be ProvLLMAuthored, got %v", got[0].Provenance)
	}
	if got[0].TrustTier != 3 {
		t.Fatalf("llm-reasoner record should be TrustTier 3, got %v", got[0].TrustTier)
	}
}

func TestStore_Record_RequiresTenant(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	err := s.Record(context.Background(), "", model.InvestigationRecord{Symptom: "x"})
	if err == nil {
		t.Fatalf("expected error recording with empty tenant id")
	}
}

// ---------------------------------------------------------------------
// AC-F08-3 / FR-F08-4: Correct() boosts retrieval weight for the
// corrected record's superseder and demotes the original.
// ---------------------------------------------------------------------

func TestStore_Correct_SupersederRankedAboveOriginal_WeightLowered(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	rec := model.InvestigationRecord{
		InvestigationID: "inv-1",
		Tenant:          tenantA,
		Symptom:         "checkout latency spike",
		RootCause:       "wrong root cause",
		Services:        []string{"checkout"},
		ErrorSignatures: []string{"timeout"},
		ReasonerKind:    model.ReasonerRules,
	}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	fp := fromTokens(tenantA, buildTokens([]string{"checkout"}, []string{"timeout"}, nil, nil))
	before, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil || len(before) != 1 {
		t.Fatalf("Similar before correction: got %d results, err=%v", len(before), err)
	}
	originalID := before[0].ID
	weightBefore := before[0].Weight

	corrected, err := s.Correct(ctx, tenantA, model.Correction{
		TargetID:    originalID,
		NewValue:    "actual root cause: connection pool exhaustion",
		CorrectedBy: "engineer@example.com",
	})
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corrected.Kind != model.RecordCorrection {
		t.Fatalf("expected corrected record Kind=Correction, got %v", corrected.Kind)
	}
	if corrected.SupersedesID != originalID {
		t.Fatalf("expected SupersedesID=%q, got %q", originalID, corrected.SupersedesID)
	}
	if corrected.Weight != 1.0 {
		t.Fatalf("expected new correction record Weight=1.0, got %v", corrected.Weight)
	}

	original, err := s.Get(ctx, tenantA, originalID)
	if err != nil {
		t.Fatalf("Get original: %v", err)
	}
	if original.Weight != weightBefore-0.35 {
		t.Fatalf("expected original weight to drop by exactly 0.35: before=%v after=%v", weightBefore, original.Weight)
	}
	if original.Corrections != 1 {
		t.Fatalf("expected Corrections=1 on original, got %d", original.Corrections)
	}

	after, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil {
		t.Fatalf("Similar after correction: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("expected superseded+superseder both returned, got %d: %+v", len(after), after)
	}
	if after[0].ID != corrected.ID {
		t.Fatalf("expected the Correction record ranked first, got %q (%v)", after[0].ID, after[0].Kind)
	}
	if after[1].ID != originalID {
		t.Fatalf("expected the original superseded record ranked second, got %q", after[1].ID)
	}
}

func TestStore_Correct_WeightFloorsAtZero(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	rec := model.InvestigationRecord{Tenant: tenantA, Symptom: "s", RootCause: "r", Services: []string{"svcx"}}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	fp := fromTokens(tenantA, buildTokens([]string{"svcx"}, nil, nil, nil))
	got, _ := s.Similar(ctx, tenantA, fp, 1)
	id := got[0].ID
	// Weight starts at 1.0; three corrections of -0.35 would go negative
	// without the floor.
	for i := 0; i < 3; i++ {
		if _, err := s.Correct(ctx, tenantA, model.Correction{TargetID: id, NewValue: "x"}); err != nil {
			t.Fatalf("Correct #%d: %v", i, err)
		}
	}
	final, err := s.Get(ctx, tenantA, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Weight != 0 {
		t.Fatalf("expected weight floored at 0, got %v", final.Weight)
	}
}

func TestStore_Confirm_BoostsWeight_CapAtTwo(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	rec := model.InvestigationRecord{Tenant: tenantA, Symptom: "s", RootCause: "r", Services: []string{"svcy"}}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	fp := fromTokens(tenantA, buildTokens([]string{"svcy"}, nil, nil, nil))
	got, _ := s.Similar(ctx, tenantA, fp, 1)
	id := got[0].ID

	if err := s.Confirm(ctx, tenantA, id, "engineer@example.com"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	r1, _ := s.Get(ctx, tenantA, id)
	if r1.Weight != 1.15 {
		t.Fatalf("expected weight 1.15 after one confirmation, got %v", r1.Weight)
	}
	if r1.Confirmations != 1 {
		t.Fatalf("expected Confirmations=1, got %d", r1.Confirmations)
	}
	// Confirm many more times; must cap at 2.0.
	for i := 0; i < 10; i++ {
		if err := s.Confirm(ctx, tenantA, id, "engineer@example.com"); err != nil {
			t.Fatalf("Confirm #%d: %v", i, err)
		}
	}
	r2, _ := s.Get(ctx, tenantA, id)
	if r2.Weight != 2.0 {
		t.Fatalf("expected weight capped at 2.0, got %v", r2.Weight)
	}
}

// effectiveWeight decay: confirm the formula matches
// Weight * 0.5^(age/90d), applied only at read time (never persisted).
func TestEffectiveWeight_DecaysButNeverPersists(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil).(*inMemoryStore)
	ctx := context.Background()
	rec := model.InvestigationRecord{Tenant: tenantA, Symptom: "s", RootCause: "r", Services: []string{"svcz"}}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var r *model.Record
	for _, v := range s.records[tenantA] {
		r = v
	}
	if r == nil {
		t.Fatalf("record not found")
	}
	storedWeight := r.Weight

	// Advance clock by exactly one half-life; effectiveWeight should
	// halve, but the persisted Weight field must not change.
	now := r.LastUsedAt.Add(decayHalfLife)
	ew := effectiveWeight(r, now)
	if diff := ew - storedWeight*0.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected effectiveWeight to halve after one half-life: got %v, want %v", ew, storedWeight*0.5)
	}
	if r.Weight != storedWeight {
		t.Fatalf("effectiveWeight must not mutate the persisted Weight field: got %v, want %v", r.Weight, storedWeight)
	}
}

// ---------------------------------------------------------------------
// AC-F08-4 / FR-F08-5: runbook import
// ---------------------------------------------------------------------

const sampleRunbook = `---
title: Checkout latency runbook
service: [checkout, payments]
symptom_tags: [timeout, latency-spike]
---
## Symptom

Checkout requests slow down under load.

## Fix

Scale the payments connection pool.
`

func TestRunbookImport_ValidFrontMatter_ParsesAndIndexes(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	imp, err := NewRunbookImporter(s)
	if err != nil {
		t.Fatalf("NewRunbookImporter: %v", err)
	}
	ctx := context.Background()

	recs, err := imp.Import(ctx, tenantA, []byte(sampleRunbook), "admin@example.com")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	rb := recs[0]
	if rb.Kind != model.RecordRunbook {
		t.Fatalf("expected Kind=Runbook, got %v", rb.Kind)
	}
	if rb.Provenance != model.ProvImported {
		t.Fatalf("expected Provenance=ProvImported, got %v", rb.Provenance)
	}
	if rb.Symptom != "Checkout latency runbook" {
		t.Fatalf("expected title parsed as Symptom, got %q", rb.Symptom)
	}

	// Interleaves with Similar() results.
	fp := fromTokens(tenantA, buildTokens([]string{"checkout", "payments"}, []string{"timeout"}, nil, nil))
	got, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil {
		t.Fatalf("Similar: %v", err)
	}
	found := false
	for _, r := range got {
		if r.ID == rb.ID && r.Kind == model.RecordRunbook {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected imported runbook to surface in Similar() results: %+v", got)
	}
}

func TestRunbookImport_MissingRequiredFields_Rejected(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	imp, err := NewRunbookImporter(s)
	if err != nil {
		t.Fatalf("NewRunbookImporter: %v", err)
	}
	bad := "---\ntitle: Missing fields\n---\nbody only\n"
	if _, err := imp.Import(context.Background(), tenantA, []byte(bad), "admin@example.com"); err == nil {
		t.Fatalf("expected error for runbook missing service/symptom_tags")
	}
}

func TestRunbookImport_ExcludedFromPruning(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	imp, err := NewRunbookImporter(s)
	if err != nil {
		t.Fatalf("NewRunbookImporter: %v", err)
	}
	ctx := context.Background()
	if _, err := imp.Import(ctx, tenantA, []byte(sampleRunbook), "admin@example.com"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	// Advance well past pruneAfter and run consolidation.
	clk.Advance(pruneAfter + 24*time.Hour)
	if _, err := s.Consolidate(ctx, clk.Now()); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	stats := s.Stats()
	if stats.Records != 1 {
		t.Fatalf("expected pinned runbook to survive consolidation prune, got %d records", stats.Records)
	}
}

// ---------------------------------------------------------------------
// AC-F08-5 / FR-F08-6, FR-F08-10: Export produces valid, reproducible
// JSON and Markdown.
// ---------------------------------------------------------------------

func TestExport_JSON_ValidAndReproducible(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		rec := model.InvestigationRecord{
			Tenant:    tenantA,
			Symptom:   "symptom",
			RootCause: "cause",
			Services:  []string{"svc"},
		}
		if err := s.Record(ctx, tenantA, rec); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}

	var b1, b2 bytes.Buffer
	if err := s.Export(ctx, tenantA, &b1, ExportJSON); err != nil {
		t.Fatalf("Export #1: %v", err)
	}
	if err := s.Export(ctx, tenantA, &b2, ExportJSON); err != nil {
		t.Fatalf("Export #2: %v", err)
	}
	if b1.String() != b2.String() {
		t.Fatalf("Export(json) not byte-reproducible across consecutive runs on an unchanged store:\n--- run1 ---\n%s\n--- run2 ---\n%s", b1.String(), b2.String())
	}

	var env exportEnvelope
	if err := json.Unmarshal(b1.Bytes(), &env); err != nil {
		t.Fatalf("Export(json) did not produce valid JSON: %v", err)
	}
	if env.Schema != exportSchema {
		t.Fatalf("expected schema %q, got %q", exportSchema, env.Schema)
	}
	if env.Count != 3 || len(env.Records) != 3 {
		t.Fatalf("expected 3 exported records, got count=%d len=%d", env.Count, len(env.Records))
	}
	for _, r := range env.Records {
		if strings.Contains(r.CreatedAt, "+") || !strings.HasSuffix(r.CreatedAt, "Z") {
			t.Fatalf("expected RFC3339 UTC timestamp ending in Z, got %q", r.CreatedAt)
		}
	}
	if strings.Contains(b1.String(), "\r\n") {
		t.Fatalf("expected LF line endings only in export output")
	}
}

func TestExport_Markdown_Valid(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	rec := model.InvestigationRecord{Tenant: tenantA, Symptom: "sym", RootCause: "cause", Services: []string{"svc"}}
	if err := s.Record(ctx, tenantA, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var b bytes.Buffer
	if err := s.Export(ctx, tenantA, &b, ExportMarkdown); err != nil {
		t.Fatalf("Export(markdown): %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "# TraceIQ Memory Export") {
		t.Fatalf("expected markdown header, got:\n%s", out)
	}
	if !strings.Contains(out, "sym") || !strings.Contains(out, "cause") {
		t.Fatalf("expected symptom/root cause text in markdown export, got:\n%s", out)
	}
	if strings.Contains(out, "\r\n") {
		t.Fatalf("expected LF line endings only in markdown export")
	}
}

// ---------------------------------------------------------------------
// FR-F08-10: Import — admin enforcement is caller's job, but ID collision
// and unknown schema handling are Store-level and tested here.
// ---------------------------------------------------------------------

func TestImport_UnknownSchema_Rejected(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	bad := `{"schema":"not.a.real.schema","records":[]}`
	_, err := s.Import(context.Background(), tenantA, strings.NewReader(bad), ExportJSON, "admin@example.com")
	if err == nil {
		t.Fatalf("expected error importing unknown schema")
	}
}

func TestImport_IDCollision_NeverOverwrites(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{Tenant: tenantA, Symptom: "original", RootCause: "r", Services: []string{"svc"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var existingID string
	for _, v := range s.(*inMemoryStore).records[tenantA] {
		existingID = v.ID
	}

	bundle := exportEnvelope{
		Schema: exportSchema,
		Tenant: tenantA,
		Records: []exportedRecord{
			{ID: existingID, Kind: uint8(model.RecordInvestigation), Symptom: "imported-collision", RootCause: "r2", CreatedAt: fmtTime(clk.Now()), LastUsedAt: fmtTime(clk.Now())},
		},
	}
	buf, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	report, err := s.Import(ctx, tenantA, bytes.NewReader(buf), ExportJSON, "admin@example.com")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Imported != 1 {
		t.Fatalf("expected 1 imported record, got %d", report.Imported)
	}
	stats := s.Stats()
	if stats.Records != 2 {
		t.Fatalf("expected original + imported (new ID) = 2 records, got %d", stats.Records)
	}
	original, err := s.Get(ctx, tenantA, existingID)
	if err != nil {
		t.Fatalf("Get original: %v", err)
	}
	if original.Symptom != "original" {
		t.Fatalf("import with colliding ID must not overwrite the existing record, got Symptom=%q", original.Symptom)
	}
}

// ---------------------------------------------------------------------
// AC-F08-6 / FR-F08-7: consolidation/dedup
// ---------------------------------------------------------------------

func TestConsolidate_MergesNearDuplicateFingerprintFamily(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	// Two records whose FingerprintTerms Jaccard is >= dedupeThreshold
	// (0.6): identical token sets trivially satisfy this.
	for i := 0; i < 2; i++ {
		if err := s.Record(ctx, tenantA, model.InvestigationRecord{
			Tenant:          tenantA,
			Symptom:         "dup",
			RootCause:       "dup cause",
			Services:        []string{"checkout", "payments"},
			ErrorSignatures: []string{"timeout"},
		}); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
		clk.Advance(time.Hour)
	}
	before := s.Stats()
	if before.Records != 2 {
		t.Fatalf("expected 2 seeded records before consolidation, got %d", before.Records)
	}

	report, err := s.Consolidate(ctx, clk.Now())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if report.Merged != 1 {
		t.Fatalf("expected 1 merge, got %d (report=%+v)", report.Merged, report)
	}
	after := s.Stats()
	if after.Records != 1 {
		t.Fatalf("expected 1 record remaining after merge, got %d", after.Records)
	}
}

func TestConsolidate_PrunesStaleNonPinnedRecords(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{Tenant: tenantA, Symptom: "old", RootCause: "c", Services: []string{"svc-stale"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	clk.Advance(pruneAfter + time.Hour)
	if _, err := s.Consolidate(ctx, clk.Now()); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if s.Stats().Records != 0 {
		t.Fatalf("expected stale non-pinned record pruned, got %d records remaining", s.Stats().Records)
	}
}

func TestConsolidate_DoesNotMergeAcrossKinds(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	imp, err := NewRunbookImporter(s)
	if err != nil {
		t.Fatalf("NewRunbookImporter: %v", err)
	}
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{
		Tenant: tenantA, Symptom: "x", RootCause: "y",
		Services: []string{"checkout", "payments"}, ErrorSignatures: []string{"timeout"},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := imp.Import(ctx, tenantA, []byte(sampleRunbook), "admin@example.com"); err != nil {
		t.Fatalf("Import runbook: %v", err)
	}
	if _, err := s.Consolidate(ctx, clk.Now()); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if s.Stats().Records != 2 {
		t.Fatalf("expected investigation and runbook to remain distinct (different Kind) after consolidation, got %d", s.Stats().Records)
	}
}

// ---------------------------------------------------------------------
// Tenant isolation — security-relevant: tenant A's records must never be
// returned for tenant B's Similar()/Search()/Get() calls, even with
// identical fingerprint token sets.
// ---------------------------------------------------------------------

func TestTenantIsolation_SimilarNeverReturnsOtherTenantsRecords(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	// Tenant A records an investigation.
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{
		Tenant:          tenantA,
		Symptom:         "tenant A secret incident",
		RootCause:       "tenant A confidential root cause",
		Services:        []string{"checkout", "payments"},
		ErrorSignatures: []string{"conn-pool-exhausted"},
	}); err != nil {
		t.Fatalf("Record (tenant A): %v", err)
	}

	// Tenant B queries with the EXACT SAME service/error-signature
	// shape. Because the fingerprint hash is computed with the tenant
	// ID inside the preimage (DR-19 §19.1), and Similar() only scans
	// the querying tenant's own record map, tenant B must get zero
	// hits even though the underlying symptom is identical.
	fpB := fromTokens(tenantB, buildTokens([]string{"checkout", "payments"}, []string{"conn-pool-exhausted"}, nil, nil))
	fpA := fromTokens(tenantA, buildTokens([]string{"checkout", "payments"}, []string{"conn-pool-exhausted"}, nil, nil))
	if fpA.Hash == fpB.Hash {
		t.Fatalf("fingerprint hashes must differ across tenants for identical tokens (tenant not in preimage): %q", fpA.Hash)
	}

	gotB, err := s.Similar(ctx, tenantB, fpB, 10)
	if err != nil {
		t.Fatalf("Similar (tenant B): %v", err)
	}
	if len(gotB) != 0 {
		t.Fatalf("SECURITY: tenant B's Similar() returned %d record(s) that belong to tenant A: %+v", len(gotB), gotB)
	}

	// Also verify tenant B querying with tenant A's own fingerprint
	// object (e.g. a forged/replayed fingerprint) still yields nothing,
	// since Similar() scopes candidate generation by the tid argument,
	// not by whatever tenant is embedded in the fp argument.
	gotB2, err := s.Similar(ctx, tenantB, fpA, 10)
	if err != nil {
		t.Fatalf("Similar (tenant B, tenant-A fingerprint): %v", err)
	}
	if len(gotB2) != 0 {
		t.Fatalf("SECURITY: tenant B's Similar() using tenant A's fingerprint object returned tenant A's data: %+v", gotB2)
	}

	// And tenant A can still find its own record.
	gotA, err := s.Similar(ctx, tenantA, fpA, 10)
	if err != nil {
		t.Fatalf("Similar (tenant A): %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("expected tenant A to retrieve its own record, got %d", len(gotA))
	}
}

func TestTenantIsolation_GetNeverCrossesTenants(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{Tenant: tenantA, Symptom: "s", RootCause: "r", Services: []string{"svc"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var id string
	for _, v := range s.(*inMemoryStore).records[tenantA] {
		id = v.ID
	}
	if _, err := s.Get(ctx, tenantB, id); err == nil {
		t.Fatalf("SECURITY: tenant B was able to Get() a record ID that belongs to tenant A")
	}
}

func TestTenantIsolation_SearchNeverCrossesTenants(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{
		Tenant: tenantA, Symptom: "checkout latency spike", RootCause: "connection pool exhaustion", Services: []string{"checkout"},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	gotB, err := s.Search(ctx, tenantB, "checkout latency spike", 10)
	if err != nil {
		t.Fatalf("Search (tenant B): %v", err)
	}
	if len(gotB) != 0 {
		t.Fatalf("SECURITY: tenant B's Search() returned tenant A's record: %+v", gotB)
	}
}

func TestTenantIsolation_CorrectAndConfirmCannotTargetOtherTenant(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{Tenant: tenantA, Symptom: "s", RootCause: "r", Services: []string{"svc"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var id string
	for _, v := range s.(*inMemoryStore).records[tenantA] {
		id = v.ID
	}
	if _, err := s.Correct(ctx, tenantB, model.Correction{TargetID: id, NewValue: "hijacked"}); err == nil {
		t.Fatalf("SECURITY: tenant B was able to Correct() a record belonging to tenant A")
	}
	if err := s.Confirm(ctx, tenantB, id, "attacker@example.com"); err == nil {
		t.Fatalf("SECURITY: tenant B was able to Confirm() a record belonging to tenant A")
	}
	// Confirm original untouched.
	orig, err := s.Get(ctx, tenantA, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if orig.Weight != 1.0 || orig.Confirmations != 0 || orig.Corrections != 0 {
		t.Fatalf("tenant A's record must be unaffected by tenant B's rejected Correct/Confirm attempts: %+v", orig)
	}
}

func TestTenantIsolation_ExportOnlyIncludesOwnTenant(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{Tenant: tenantA, Symptom: "a-only", RootCause: "r", Services: []string{"svc"}}); err != nil {
		t.Fatalf("Record (A): %v", err)
	}
	if err := s.Record(ctx, tenantB, model.InvestigationRecord{Tenant: tenantB, Symptom: "b-only", RootCause: "r", Services: []string{"svc"}}); err != nil {
		t.Fatalf("Record (B): %v", err)
	}
	var b bytes.Buffer
	if err := s.Export(ctx, tenantA, &b, ExportJSON); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if strings.Contains(b.String(), "b-only") {
		t.Fatalf("SECURITY: tenant A's export contained tenant B's data:\n%s", b.String())
	}
	var env exportEnvelope
	if err := json.Unmarshal(b.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Count != 1 {
		t.Fatalf("expected exactly 1 record (tenant A's own), got %d", env.Count)
	}
}

// ---------------------------------------------------------------------
// Bounded candidate generation (FR-F08-2 / AC-F08-7's non-latency half)
// ---------------------------------------------------------------------

func TestSimilar_BoundedByMaxCandidates(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()
	// Seed more than maxCandidates records; Similar must not choke and
	// must still return a bounded, sensible result set.
	for i := 0; i < maxCandidates+50; i++ {
		if err := s.Record(ctx, tenantA, model.InvestigationRecord{
			Tenant: tenantA, Symptom: "s", RootCause: "r",
			Services: []string{"svc-common"},
		}); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
	fp := fromTokens(tenantA, buildTokens([]string{"svc-common"}, nil, nil, nil))
	got, err := s.Similar(ctx, tenantA, fp, 10)
	if err != nil {
		t.Fatalf("Similar: %v", err)
	}
	if len(got) > 10 {
		t.Fatalf("expected topK=10 to bound results, got %d", len(got))
	}
}

// TestSimilar_CandidateCutKeepsBestMatch_NotArbitrarySubset guards against a
// regression of a real bug found in review: candidate generation used to
// stop at maxCandidates by breaking out of a Go map range loop, whose
// iteration order is randomized. Once a tenant holds more than
// maxCandidates records, that could silently and non-deterministically
// evict the actual best (or only) match in favor of unrelated records that
// merely happened to be visited first — violating FR-F08-2/DR-19 §19.2's
// "rarest-first keeps the SELECTIVE candidates" requirement. This seeds
// more than maxCandidates completely unrelated ("noise") records plus
// exactly one record that shares the query's fingerprint tokens, and
// asserts the shared-token record is always found — which only holds if
// candidate selection is overlap-aware rather than an arbitrary map-order
// cut.
func TestSimilar_CandidateCutKeepsBestMatch_NotArbitrarySubset(t *testing.T) {
	clk := mustClock()
	s := NewInMemoryStore(clk, nil)
	ctx := context.Background()

	for i := 0; i < maxCandidates+50; i++ {
		if err := s.Record(ctx, tenantA, model.InvestigationRecord{
			Tenant: tenantA, Symptom: "noise", RootCause: "noise",
			Services: []string{"svc-noise"}, // zero token overlap with the query below
		}); err != nil {
			t.Fatalf("Record noise #%d: %v", i, err)
		}
	}
	if err := s.Record(ctx, tenantA, model.InvestigationRecord{
		Tenant: tenantA, Symptom: "the actual match", RootCause: "conn pool exhausted",
		Services: []string{"checkout"}, ErrorSignatures: []string{"conn-pool-exhausted"},
	}); err != nil {
		t.Fatalf("Record signal: %v", err)
	}

	fp := fromTokens(tenantA, buildTokens([]string{"checkout"}, []string{"conn-pool-exhausted"}, nil, nil))
	got, err := s.Similar(ctx, tenantA, fp, 5)
	if err != nil {
		t.Fatalf("Similar: %v", err)
	}
	if len(got) != 1 || got[0].Symptom != "the actual match" {
		t.Fatalf("expected the single true match to survive candidate-cut among %d noise records, got %d results: %+v", maxCandidates+50, len(got), got)
	}
}
