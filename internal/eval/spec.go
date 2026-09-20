package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"traceiq/internal/model"

	yaml "go.yaml.in/yaml/v3"
)

// --- YAML wire format ---------------------------------------------------
//
// Mirrors FR-F11-1's field list verbatim: id, title, tenant, fixture,
// baseline_warmup, clock (start/fault_at/end), faults (ModeLive only), and
// expect (incident_within, epicenter_service, root_cause_category,
// root_cause_component, expected_evidence, max_cost_micro_usd,
// must_not_page). Kept as private structs, distinct from the public
// ScenarioSpec (eval.go, DR-36 §36.1 verbatim), so YAML tags/string-enum
// wire values never leak onto the typed struct the rest of the package
// compiles against.

type specYAML struct {
	ID             string       `yaml:"id"`
	Title          string       `yaml:"title"`
	Description    string       `yaml:"description"`
	Tenant         string       `yaml:"tenant"`
	Fixture        fixtureYAML  `yaml:"fixture"`
	BaselineWarmup *fixtureYAML `yaml:"baseline_warmup"`
	Clock          clockYAML    `yaml:"clock"`
	Faults         []faultYAML  `yaml:"faults"`
	Expect         expectYAML   `yaml:"expect"`
}

type fixtureYAML struct {
	Path        string `yaml:"path"`
	LogsPath    string `yaml:"logs_path"`
	MetricsPath string `yaml:"metrics_path"`
}

type clockYAML struct {
	Start   time.Time `yaml:"start"`
	FaultAt time.Time `yaml:"fault_at"`
	End     time.Time `yaml:"end"`
}

// faultYAML is ModeLive-only (FR-F11-1); this wave's Runner only implements
// ModeOffline (see doc.go's allowed-imports note and w13's report), so a
// non-empty faults list is rejected by validateSpec with a clear error
// rather than silently accepted and ignored.
type faultYAML struct {
	Type string `yaml:"type"`
}

type expectYAML struct {
	IncidentWithin     string   `yaml:"incident_within"`
	EpicenterService   string   `yaml:"epicenter_service"`
	RootCauseCategory  string   `yaml:"root_cause_category"`
	RootCauseComponent string   `yaml:"root_cause_component"`
	ExpectedEvidence   []string `yaml:"expected_evidence"`
	MaxCostMicroUSD    int64    `yaml:"max_cost_micro_usd"`
	MustNotPage        bool     `yaml:"must_not_page"`
}

// LoadSpec parses and validates one ScenarioSpec YAML file (FR-F11-1).
// Fixture paths are resolved relative to the spec file's own directory, so
// scenario suites are relocatable as a unit.
func LoadSpec(path string) (ScenarioSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ScenarioSpec{}, fmt.Errorf("eval: read scenario spec %s: %w", path, err)
	}
	var wire specYAML
	if err := yaml.Unmarshal(raw, &wire); err != nil {
		return ScenarioSpec{}, fmt.Errorf("eval: parse scenario spec %s: %w", path, err)
	}
	spec, err := toScenarioSpec(wire, filepath.Dir(path))
	if err != nil {
		return ScenarioSpec{}, fmt.Errorf("eval: invalid scenario spec %s: %w", path, err)
	}
	return spec, nil
}

// List loads every *.yaml scenario spec under dir (sorted by filename for
// deterministic ordering), optionally restricted to the given ids
// (Select; empty means all). Each file is independently loadable (AC-F11-3):
// one malformed file's error names that file rather than aborting silently.
func List(dir string, selectIDs []string) ([]ScenarioSpec, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: read scenarios dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".yaml" || filepath.Ext(e.Name()) == ".yml" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)

	want := map[string]bool{}
	for _, id := range selectIDs {
		want[id] = true
	}

	specs := make([]ScenarioSpec, 0, len(files))
	for _, f := range files {
		spec, err := LoadSpec(f)
		if err != nil {
			return nil, err
		}
		if len(want) > 0 && !want[spec.ID] {
			continue
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func toScenarioSpec(w specYAML, baseDir string) (ScenarioSpec, error) {
	if w.ID == "" {
		return ScenarioSpec{}, fmt.Errorf("missing required field %q", "id")
	}
	if w.Title == "" {
		return ScenarioSpec{}, fmt.Errorf("missing required field %q", "title")
	}
	if w.Tenant == "" {
		return ScenarioSpec{}, fmt.Errorf("missing required field %q", "tenant")
	}
	if w.Fixture.Path == "" {
		return ScenarioSpec{}, fmt.Errorf("missing required field %q", "fixture.path")
	}
	if len(w.Faults) > 0 {
		return ScenarioSpec{}, fmt.Errorf("faults present but ModeLive is out of scope this wave (w13): %d fault(s) given", len(w.Faults))
	}

	expect, err := toExpectation(w.Expect)
	if err != nil {
		return ScenarioSpec{}, err
	}

	spec := ScenarioSpec{
		ID:          w.ID,
		Title:       w.Title,
		Description: w.Description,
		Tenant:      model.TenantID(w.Tenant),
		Fixture:     resolveFixture(w.Fixture, baseDir),
		Clock: ClockSpec{
			Start:   w.Clock.Start,
			FaultAt: w.Clock.FaultAt,
			End:     w.Clock.End,
		},
		Expect: expect,
	}
	if w.BaselineWarmup != nil {
		f := resolveFixture(*w.BaselineWarmup, baseDir)
		spec.BaselineWarmup = &f
	}
	return spec, nil
}

func resolveFixture(f fixtureYAML, baseDir string) FixtureRef {
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(baseDir, p)
	}
	return FixtureRef{
		Path:        resolve(f.Path),
		LogsPath:    resolve(f.LogsPath),
		MetricsPath: resolve(f.MetricsPath),
	}
}

func toExpectation(w expectYAML) (Expectation, error) {
	if w.EpicenterService == "" {
		return Expectation{}, fmt.Errorf("missing required field %q", "expect.epicenter_service")
	}
	if w.RootCauseCategory == "" {
		return Expectation{}, fmt.Errorf("missing required field %q", "expect.root_cause_category")
	}
	cat, err := categoryFromString(w.RootCauseCategory)
	if err != nil {
		return Expectation{}, err
	}
	if w.IncidentWithin == "" {
		return Expectation{}, fmt.Errorf("missing required field %q", "expect.incident_within")
	}
	incidentWithin, err := time.ParseDuration(w.IncidentWithin)
	if err != nil {
		return Expectation{}, fmt.Errorf("expect.incident_within: %w", err)
	}

	evidence := make([]model.EvidenceCategory, 0, len(w.ExpectedEvidence))
	for _, e := range w.ExpectedEvidence {
		ec, err := evidenceFromString(e)
		if err != nil {
			return Expectation{}, err
		}
		evidence = append(evidence, ec)
	}

	return Expectation{
		IncidentWithin:     incidentWithin,
		EpicenterService:   w.EpicenterService,
		RootCauseCategory:  cat,
		RootCauseComponent: w.RootCauseComponent,
		ExpectedEvidence:   evidence,
		MaxCostMicroUSD:    w.MaxCostMicroUSD,
		MustNotPage:        w.MustNotPage,
	}, nil
}
