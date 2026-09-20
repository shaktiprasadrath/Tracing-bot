package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"traceiq/internal/model"
	"traceiq/internal/rca"
)

// fixtureRegistry is a rca.ToolRegistry (DR-15, "consumer-declared" per
// DR-2) backed entirely by one scenario's fixture bundle: every Dispatch
// call, regardless of which of the five tools it names, returns the same
// canned model.ToolResult.Rows loaded from ScenarioSpec.Fixture.Path.
//
// This is this wave's deliberate simplification of FR-F11-2's "replay
// through the real ingest.Receiver" requirement: no ingest.Receiver,
// sampler, or anomaly.Detector wiring exists yet for eval to drive (that is
// out of scope per the task brief — "replayed trace fixtures, no live
// cluster required"). What IS real and unmocked is everything downstream of
// incident formation: rca.NewEngine's actual loop (budget accounting, step
// journaling, hypothesis merge) and rules.Reasoner's actual catalog
// (NextStep/Confirm/Conclude) run unmodified against this canned data. A
// rule's Confirm predicate only returns true for rows shaped like its own
// row type (see internal/rca/rules), so returning the same bytes to every
// call is safe: a mismatched rule's Confirm simply reports "not confirmed"
// (decodeRows fails the type-shape) rather than a false positive, and the
// catalog's fixed priority order still determines which rule wins when more
// than one could apply. Deferred to a later wave: full ingest.Receiver
// replay and FR-F11-13's receiver-entry test hook (see
// docs/reports/w13-eval.md).
type fixtureRegistry struct {
	rows []byte
}

func newFixtureRegistry(rows []byte) *fixtureRegistry {
	return &fixtureRegistry{rows: rows}
}

func (f *fixtureRegistry) Get(model.ToolName) (rca.Tool, bool) { return nil, false }

func (f *fixtureRegistry) Names() []model.ToolName {
	return []model.ToolName{
		rca.ToolTraceQuery, rca.ToolLogQuery, rca.ToolMetricQuery,
		rca.ToolTopologyQuery, rca.ToolMemoryQuery,
	}
}

func (f *fixtureRegistry) Dispatch(_ context.Context, _ model.TenantID, a rca.ToolArgs) (model.ToolResult, error) {
	return model.ToolResult{Rows: f.rows, Tool: a.Tool}, nil
}

var _ rca.ToolRegistry = (*fixtureRegistry)(nil)

// loadFixtureRows reads a scenario's canned tool-result rows (raw JSON
// bytes, already shaped like one of internal/rca/rules' row types) from
// FixtureRef.Path.
func loadFixtureRows(ref FixtureRef) ([]byte, error) {
	if ref.Path == "" {
		return nil, fmt.Errorf("eval: fixture path is empty")
	}
	b, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, fmt.Errorf("eval: read fixture %s: %w", ref.Path, err)
	}
	return b, nil
}

// buildIncident derives a model.Incident from a ScenarioSpec's Expect block
// (FR-F11-4's inputs) plus its virtual Clock — this wave has no
// anomaly.Detector wired to produce one, so the harness constructs the
// incident directly, exactly the way an eval fixture's baseline_warmup +
// fault window would resolve to one in a fully wired pipeline.
func buildIncident(spec ScenarioSpec, tenant model.TenantID) model.Incident {
	firstSeen := spec.Clock.FaultAt
	if firstSeen.IsZero() {
		firstSeen = spec.Clock.Start
	}
	return model.Incident{
		ID:               spec.ID + "-incident",
		Tenant:           tenant,
		Title:            spec.Title,
		Status:           model.IncidentInvestigating,
		Services:         []string{spec.Expect.EpicenterService},
		EpicenterService: spec.Expect.EpicenterService,
		BlastRadius:      []string{spec.Expect.EpicenterService},
		Fingerprint:      "fp1:" + hex.EncodeToString(sha256.New().Sum([]byte(spec.ID)))[:16],
		FirstSeen:        firstSeen,
		LastSeen:         spec.Clock.End,
		CreatedAt:        firstSeen,
	}
}

// investigate drives one scenario end-to-end through the real rca.Engine +
// the injected rca.Reasoner — see fixtureRegistry's doc comment for exactly
// what is and is not real here. It returns the completed
// model.Investigation together with the model.Incident used to start it
// (TimeToRCAMillis needs Incident.FirstSeen).
//
// The reasoner is a caller-supplied rca.Reasoner rather than a
// package-level rules.New() call: DR-2's adjacency table (internal/archtest)
// permits "eval" to import "rca" but NOT "rca/rules" — the concrete rules
// (or llm) Reasoner is constructed by eval's caller, exactly like
// rca.NewEngine itself takes Reasoner as a constructor parameter rather
// than choosing one internally. This wave's tests inject rules.New()
// directly (test files are exempt from the adjacency check) to drive a
// real, non-mocked investigation end-to-end; see runner.go's NewRunner.
func investigate(ctx context.Context, spec ScenarioSpec, tenant model.TenantID, reasoner rca.Reasoner) (model.Investigation, model.Incident, error) {
	rows, err := loadFixtureRows(spec.Fixture)
	if err != nil {
		return model.Investigation{}, model.Incident{}, err
	}
	incident := buildIncident(spec, tenant)

	journal := rca.NewMemJournal()
	objects := rca.NewMemObjectStore()
	registry := newFixtureRegistry(rows)

	eng := rca.NewEngine(journal, registry, reasoner, objects, nil, nil)
	inv, err := eng.Investigate(ctx, tenant, incident)
	if err != nil {
		return model.Investigation{}, incident, fmt.Errorf("eval: investigate scenario %s: %w", spec.ID, err)
	}
	return inv, incident, nil
}
