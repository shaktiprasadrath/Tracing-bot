package eval

import (
	"fmt"
	"sort"
	"strings"

	"traceiq/internal/model"
)

// categoryNames is the YAML wire vocabulary for model.HypothesisCategory
// (DR-15's closed enum). A ScenarioSpec's expect.root_cause_category field
// must be one of these strings; anything else is a validation error naming
// the closed set, per FR-F11-1's "clear validation error" requirement.
var categoryNames = map[string]model.HypothesisCategory{
	"saturation":          model.CatSaturation,
	"dependency_failure":  model.CatDependencyFailure,
	"deploy_regression":   model.CatDeployRegression,
	"config_change":       model.CatConfigChange,
	"resource_exhaustion": model.CatResourceExhaustion,
	"network":             model.CatNetwork,
	"data_skew":           model.CatDataSkew,
	"external_provider":   model.CatExternalProvider,
}

func categoryFromString(s string) (model.HypothesisCategory, error) {
	if c, ok := categoryNames[s]; ok {
		return c, nil
	}
	return 0, fmt.Errorf("eval: unknown root_cause_category %q (want one of: %s)", s, joinedCategoryNames())
}

func joinedCategoryNames() string {
	names := make([]string, 0, len(categoryNames))
	for n := range categoryNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// evidenceNames is the YAML wire vocabulary for model.EvidenceCategory
// (DR-36 §36.4's closed enum, defined once in internal/model).
var evidenceNames = map[string]model.EvidenceCategory{
	"trace_exemplar":  model.EvTraceExemplar,
	"error_signature": model.EvErrorSignature,
	"log_line":        model.EvLogLine,
	"metric_series":   model.EvMetricSeries,
	"topology_edge":   model.EvTopologyEdge,
	"deploy_marker":   model.EvDeployMarker,
	"red_series":      model.EvREDSeries,
	"memory_record":   model.EvMemoryRecord,
}

func evidenceFromString(s string) (model.EvidenceCategory, error) {
	if c, ok := evidenceNames[s]; ok {
		return c, nil
	}
	return 0, fmt.Errorf("eval: unknown expected_evidence category %q (want one of: %s)", s, joinedEvidenceNames())
}

func joinedEvidenceNames() string {
	names := make([]string, 0, len(evidenceNames))
	for n := range evidenceNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
