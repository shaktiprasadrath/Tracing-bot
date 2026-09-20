package memory

import (
	"context"
	"fmt"
	"strings"

	"traceiq/internal/model"
)

// Runbook is F08 §4.2's locally-declared type ("`Runbook` (`Title`,
// `ServiceTags`, `SymptomTags`, `Body`, `SourceFile`) remain local to
// internal/memory — DR-19 does not move them").
type Runbook struct {
	Title       string
	ServiceTags []string
	SymptomTags []string
	Body        string
	SourceFile  string
}

// inMemoryRunbookImporter is FR-F08-5's RunbookImporter, backed by the same
// in-memory tenant maps as inMemoryStore (see store.go's type doc for why
// this pass is in-memory rather than SQLite). It requires a Store built by
// NewInMemoryStore — NewRunbookImporter rejects any other Store
// implementation rather than silently no-op-ing.
type inMemoryRunbookImporter struct {
	store *inMemoryStore
}

// NewRunbookImporter constructs FR-F08-5's RunbookImporter over an
// in-memory Store. DR-19 §19.3's Store interface has no "insert a raw
// Kind=Runbook record" method (and the interface is bound verbatim, so this
// pass does not add one) — RunbookImporter therefore type-asserts down to
// the concrete *inMemoryStore built by NewInMemoryStore to reach the
// tenant-partitioned maps directly. A future SQLite-backed Store would give
// RunbookImporter its own DB handle instead of this type assertion.
func NewRunbookImporter(s Store) (RunbookImporter, error) {
	ims, ok := s.(*inMemoryStore)
	if !ok {
		return nil, fmt.Errorf("memory: RunbookImporter requires a Store built by NewInMemoryStore, got %T", s)
	}
	return &inMemoryRunbookImporter{store: ims}, nil
}

// parseRunbookFrontMatter splits a runbook Markdown file into its
// YAML-ish front-matter block and body, per FR-F08-5 / §4.4's
// "Runbook import" pseudocode. Only the minimal subset of YAML this
// pass's fixtures need is supported: `key: value` scalars and
// `key: [a, b, c]` flow-sequence lists — a full YAML parser is not pulled
// in given memory's allowed-import list (model, config, tenant, store,
// llm) and this pass's time budget; documented deviation.
func parseRunbookFrontMatter(markdown []byte) (title string, services, symptomTags []string, body string, err error) {
	text := strings.ReplaceAll(string(markdown), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", nil, nil, "", fmt.Errorf("memory: runbook missing opening front-matter delimiter (---)")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", nil, nil, "", fmt.Errorf("memory: runbook front matter not terminated (missing closing ---)")
	}
	fm := rest[:end]
	after := rest[end+len("\n---"):]
	after = strings.TrimPrefix(after, "\n")
	body = strings.TrimSpace(after)

	scalars := map[string]string{}
	lists := map[string][]string{}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(val, "["), "]"))
			var items []string
			if inner != "" {
				for _, it := range strings.Split(inner, ",") {
					it = strings.Trim(strings.TrimSpace(it), `"'`)
					if it != "" {
						items = append(items, it)
					}
				}
			}
			lists[key] = items
		} else {
			scalars[key] = strings.Trim(val, `"'`)
		}
	}

	title = scalars["title"]
	if svc, ok := lists["service"]; ok {
		services = svc
	} else if v, ok := scalars["service"]; ok && v != "" {
		services = []string{v}
	}
	if tags, ok := lists["symptom_tags"]; ok {
		symptomTags = tags
	} else if v, ok := scalars["symptom_tags"]; ok && v != "" {
		symptomTags = []string{v}
	}

	if title == "" || len(services) == 0 || len(symptomTags) == 0 {
		return "", nil, nil, "", fmt.Errorf("memory: runbook front matter missing required field(s): title=%q service=%v symptom_tags=%v", title, services, symptomTags)
	}
	return title, services, symptomTags, body, nil
}

// Import is FR-F08-5 / §4.4's runbook-import pseudocode: parse front
// matter, validate required fields, compute a fingerprint over the closed
// token classes (service -> svc:, symptom_tags -> errsig:, sharing
// buildTokens with Store.Record so runbooks land in the same
// fingerprint-similarity space as investigations, FR-F08-5), and persist a
// Kind=Runbook, Provenance=ProvImported record.
//
// Deviation: the spec's secretScrubber.Scrub(body) step is not applied —
// memory's allowed imports (model, config, tenant, store, llm) name no
// scrubber package, and F06/F07's scrubber lives outside that adjacency
// list. Documented, not silently dropped.
func (imp *inMemoryRunbookImporter) Import(ctx context.Context, tid model.TenantID, markdown []byte, by string) ([]model.Record, error) {
	if tid == "" {
		return nil, fmt.Errorf("memory: tenant id is required")
	}
	title, services, symptomTags, body, err := parseRunbookFrontMatter(markdown)
	if err != nil {
		return nil, err
	}

	s := imp.store
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	terms := buildTokens(services, symptomTags, nil, nil)
	fp := hashTokens(tid, terms)
	r := &model.Record{
		ID:               s.nextID("run"),
		Tenant:           tid,
		Kind:             model.RecordRunbook,
		Fingerprint:      fp,
		FingerprintTerms: terms,
		Symptom:          title,
		RootCause:        body,
		Services:         append([]string{}, services...),
		ErrorSignatures:  append([]string{}, symptomTags...),
		Weight:           1.0,
		Provenance:       model.ProvImported,
		TrustTier:        2,
		BodyMarkdown:     body,
		CreatedAt:        now,
		UpdatedAt:        now,
		LastUsedAt:       now,
	}
	_ = by // FR-F08-5's admin-only enforcement is an API-layer concern; this Store-level method trusts its caller (same posture as Store.Import).
	s.tenantMap(tid)[r.ID] = r
	return []model.Record{*r}, nil
}
