package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceiq/internal/model"
)

const testdataScenariosDir = "testdata/scenarios"

// TestLoadSpec_Valid covers FR-F11-1: a valid spec round-trips into typed
// fields, including string-enum resolution (root_cause_category) and
// duration parsing (incident_within), and fixture paths resolved relative
// to the spec file's own directory.
func TestLoadSpec_Valid(t *testing.T) {
	spec, err := LoadSpec(filepath.Join(testdataScenariosDir, "S01-error-signature-after-deploy.yaml"))
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec.ID != "S01-error-signature-after-deploy" {
		t.Errorf("ID = %q", spec.ID)
	}
	if spec.Tenant != model.TenantID("eval-tenant") {
		t.Errorf("Tenant = %q", spec.Tenant)
	}
	if spec.Expect.RootCauseCategory != model.CatDeployRegression {
		t.Errorf("RootCauseCategory = %v, want CatDeployRegression", spec.Expect.RootCauseCategory)
	}
	if spec.Expect.IncidentWithin != 5*time.Minute {
		t.Errorf("IncidentWithin = %v, want 5m", spec.Expect.IncidentWithin)
	}
	if spec.Expect.EpicenterService != "checkout" {
		t.Errorf("EpicenterService = %q", spec.Expect.EpicenterService)
	}
	if !filepath.IsAbs(spec.Fixture.Path) && !strings.Contains(spec.Fixture.Path, "deploy_regression.json") {
		t.Errorf("Fixture.Path = %q, want it to resolve to deploy_regression.json", spec.Fixture.Path)
	}
	if _, err := os.Stat(spec.Fixture.Path); err != nil {
		t.Errorf("resolved fixture path does not exist: %v", err)
	}
}

// TestLoadSpec_Malformed covers FR-F11-1's "missing required fields
// rejected with a clear validation error" requirement, table-driven over
// several distinct malformations.
func TestLoadSpec_Malformed(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing id",
			yaml: `
title: t
tenant: eval-tenant
fixture: {path: f.json}
expect: {incident_within: 5m, epicenter_service: checkout, root_cause_category: deploy_regression}
`,
			wantErr: `"id"`,
		},
		{
			name: "missing fixture path",
			yaml: `
id: s1
title: t
tenant: eval-tenant
fixture: {}
expect: {incident_within: 5m, epicenter_service: checkout, root_cause_category: deploy_regression}
`,
			wantErr: `"fixture.path"`,
		},
		{
			name: "unknown root_cause_category",
			yaml: `
id: s1
title: t
tenant: eval-tenant
fixture: {path: f.json}
expect: {incident_within: 5m, epicenter_service: checkout, root_cause_category: not_a_real_category}
`,
			wantErr: "unknown root_cause_category",
		},
		{
			name: "unparseable incident_within",
			yaml: `
id: s1
title: t
tenant: eval-tenant
fixture: {path: f.json}
expect: {incident_within: not-a-duration, epicenter_service: checkout, root_cause_category: deploy_regression}
`,
			wantErr: "incident_within",
		},
		{
			name: "faults present (ModeLive out of scope)",
			yaml: `
id: s1
title: t
tenant: eval-tenant
fixture: {path: f.json}
faults:
  - type: restart_pod
expect: {incident_within: 5m, epicenter_service: checkout, root_cause_category: deploy_regression}
`,
			wantErr: "ModeLive is out of scope",
		},
		{
			name:    "not valid YAML at all",
			yaml:    "{{{ not: yaml: at: all",
			wantErr: "parse scenario spec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadSpec(path)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestList covers AC-F11-3's shape (each scenario independently loadable
// via List) against this wave's 3-scenario testdata suite, plus Select
// filtering.
func TestList(t *testing.T) {
	specs, err := List(testdataScenariosDir, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}
	// Sorted by filename.
	if specs[0].ID != "S01-error-signature-after-deploy" {
		t.Errorf("specs[0].ID = %q", specs[0].ID)
	}

	filtered, err := List(testdataScenariosDir, []string{"S02-downstream-latency-propagation"})
	if err != nil {
		t.Fatalf("List (filtered): %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "S02-downstream-latency-propagation" {
		t.Fatalf("filtered = %+v, want exactly S02", filtered)
	}
}

func TestList_UnknownDir(t *testing.T) {
	if _, err := List(filepath.Join(t.TempDir(), "does-not-exist"), nil); err == nil {
		t.Fatal("expected an error for a nonexistent scenarios dir")
	}
}
