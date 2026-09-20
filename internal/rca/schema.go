package rca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"traceiq/internal/model"
)

// --- DR-37 §37.1(4): strict output schema ---
//
// llmProposalWire is the exact JSON shape the model's structured output MUST
// match (F06 §4.4's NextStep_llm: "typed schema"; DR-37 §37.1(4): "rejects
// unknown fields, extra keys and any value outside a closed enum. There is
// no best-effort parse."). It is deliberately NOT rca.Proposal itself:
// Proposal.Call is a ToolArgs (a Go struct with unexported invariants like
// "exactly one payload pointer set") that a hostile or malformed model
// response must never be unmarshaled directly into — the wire type is
// parsed first, byte-for-byte, and only a value that survives every check in
// validateReasonerOutput is translated into a Proposal.
type llmProposalWire struct {
	Phase      string              `json:"phase"`
	Done       bool                `json:"done"`
	Rationale  string              `json:"rationale"`
	Hypotheses []llmHypothesisWire `json:"hypotheses"`
	ToolCall   *llmToolCallWire    `json:"tool_call"`
}

type llmHypothesisWire struct {
	ID        string  `json:"id"`
	Statement string  `json:"statement"`
	Category  string  `json:"category"`
	Component string  `json:"component"`
	PostScore float64 `json:"post_score"`
	Status    string  `json:"status"`
}

// llmToolCallWire mirrors ToolArgs's closed-union shape (DR-16 §16.2): the
// model names exactly one of the five tools and supplies exactly the
// matching typed payload. Reusing the same typed row structs as ToolArgs
// (TraceQueryArgs etc.) means "no free-form string, no map[string]any"
// applies to the model's own JSON, not just to rca-internal call sites.
type llmToolCallWire struct {
	Tool     string             `json:"tool"`
	Trace    *TraceQueryArgs    `json:"trace,omitempty"`
	Log      *LogQueryArgs      `json:"log,omitempty"`
	Metric   *MetricQueryArgs   `json:"metric,omitempty"`
	Topology *TopologyQueryArgs `json:"topology,omitempty"`
	Memory   *MemoryQueryArgs   `json:"memory,omitempty"`
}

// Length ceilings for model-authored Hypothesis free-text fields — see the
// w16 review comment at their call site in ValidateReasonerOutput.
const (
	maxHypothesisIDBytes        = 128
	maxHypothesisStatementBytes = 500
	maxHypothesisComponentBytes = 200
)

var (
	validPhases = map[string]model.Phase{
		"contextualize": model.PhaseContextualize,
		"hypothesize":   model.PhaseHypothesize,
		"test":          model.PhaseTest,
		"validate":      model.PhaseValidate,
		"report":        model.PhaseReport,
	}
	validCategories = map[string]model.HypothesisCategory{
		"saturation":          model.CatSaturation,
		"dependency_failure":  model.CatDependencyFailure,
		"deploy_regression":   model.CatDeployRegression,
		"config_change":       model.CatConfigChange,
		"resource_exhaustion": model.CatResourceExhaustion,
		"network":             model.CatNetwork,
		"data_skew":           model.CatDataSkew,
		"external_provider":   model.CatExternalProvider,
	}
	validStatuses = map[string]model.HypothesisStatus{
		"proposed":     model.HypothesisProposed,
		"testing":      model.HypothesisTesting,
		"supported":    model.HypothesisSupported,
		"refuted":      model.HypothesisRefuted,
		"inconclusive": model.HypothesisInconclusive,
	}
	validTools = map[string]model.ToolName{
		string(ToolTraceQuery):    ToolTraceQuery,
		string(ToolLogQuery):      ToolLogQuery,
		string(ToolMetricQuery):   ToolMetricQuery,
		string(ToolTopologyQuery): ToolTopologyQuery,
		string(ToolMemoryQuery):   ToolMemoryQuery,
	}
)

// schemaValidator is rca.SchemaValidator's (DR-15) default implementation.
type schemaValidator struct{}

func newSchemaValidator() *schemaValidator { return &schemaValidator{} }

// ValidateReasonerOutput is DR-37 §37.1(4): strict — an unknown field is an
// error, every enum value must be one of the closed set, and the tool-call
// payload (if any) must match ToolArgs's exactly-one-set invariant.
func (schemaValidator) ValidateReasonerOutput(raw []byte) (Proposal, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Proposal{}, fmt.Errorf("rca: empty reasoner output")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var wire llmProposalWire
	if err := dec.Decode(&wire); err != nil {
		return Proposal{}, fmt.Errorf("rca: reasoner output failed strict decode: %w", err)
	}
	if dec.More() {
		return Proposal{}, fmt.Errorf("rca: reasoner output has trailing data after the JSON value")
	}

	phase, ok := validPhases[wire.Phase]
	if !ok {
		return Proposal{}, fmt.Errorf("rca: reasoner output has unknown phase %q", wire.Phase)
	}
	if len(wire.Rationale) > 2000 {
		return Proposal{}, fmt.Errorf("rca: reasoner rationale exceeds 2000 bytes")
	}

	hyps := make([]model.Hypothesis, 0, len(wire.Hypotheses))
	for _, h := range wire.Hypotheses {
		cat, ok := validCategories[h.Category]
		if !ok {
			return Proposal{}, fmt.Errorf("rca: hypothesis %q has unknown category %q", h.ID, h.Category)
		}
		status, ok := validStatuses[h.Status]
		if !ok {
			return Proposal{}, fmt.Errorf("rca: hypothesis %q has unknown status %q", h.ID, h.Status)
		}
		if h.ID == "" {
			return Proposal{}, fmt.Errorf("rca: hypothesis missing id")
		}
		// w16 security review: Statement/Component/ID are model-authored free
		// text that survives into Investigation.Hypotheses and is re-rendered,
		// unwrapped, into every subsequent turn's prompt (buildPrompt's
		// "Hypotheses on the board so far" block) — an unbounded string here
		// is both a token/cost-budget amplification vector (DR-17 §17.1's
		// worked arithmetic assumes bounded per-step material) and, since it
		// is model-authored rather than TraceIQ-authored, exactly the class
		// of string DR-37 §37.1(2) requires to be bounded/controlled before
		// it round-trips back into context the model treats as trusted board
		// state. Limits chosen well above any legitimate value: a real
		// statement/component is a short sentence/service name, not a
		// multi-KB blob.
		if len(h.ID) > maxHypothesisIDBytes {
			return Proposal{}, fmt.Errorf("rca: hypothesis id exceeds %d bytes", maxHypothesisIDBytes)
		}
		if len(h.Statement) > maxHypothesisStatementBytes {
			return Proposal{}, fmt.Errorf("rca: hypothesis %q statement exceeds %d bytes", h.ID, maxHypothesisStatementBytes)
		}
		if len(h.Component) > maxHypothesisComponentBytes {
			return Proposal{}, fmt.Errorf("rca: hypothesis %q component exceeds %d bytes", h.ID, maxHypothesisComponentBytes)
		}
		// PostScore feeds Investigation.Confidence directly (LLMReasoner.
		// Conclude) and is compared against rca.confidence_threshold
		// (errors.go's confidenceThreshold) to decide Concluded vs
		// Inconclusive — an out-of-[0,1] value is not a valid probability/
		// score under any reading of DR-15 and, left unchecked, lets a
		// hallucinated or injection-influenced response force a spuriously
		// "confident" conclusion (e.g. post_score: 999) or corrupt the
		// best-hypothesis comparison with a nonsensical negative value.
		if h.PostScore < 0 || h.PostScore > 1 {
			return Proposal{}, fmt.Errorf("rca: hypothesis %q post_score %v out of range [0,1]", h.ID, h.PostScore)
		}
		hyps = append(hyps, model.Hypothesis{
			ID:        h.ID,
			Statement: h.Statement,
			Category:  cat,
			Component: h.Component,
			PostScore: h.PostScore,
			Status:    status,
			Source:    model.HypSourceLLM,
		})
	}

	prop := Proposal{
		Phase:      phase,
		Hypotheses: hyps,
		Done:       wire.Done,
		Rationale:  wire.Rationale,
	}

	if wire.ToolCall != nil {
		call, err := decodeToolCallWire(*wire.ToolCall)
		if err != nil {
			return Proposal{}, err
		}
		prop.Call = &call
	}

	return prop, nil
}

func decodeToolCallWire(w llmToolCallWire) (ToolArgs, error) {
	tool, ok := validTools[w.Tool]
	if !ok {
		return ToolArgs{}, fmt.Errorf("rca: reasoner proposed unknown tool %q", w.Tool)
	}
	set := 0
	if w.Trace != nil {
		set++
	}
	if w.Log != nil {
		set++
	}
	if w.Metric != nil {
		set++
	}
	if w.Topology != nil {
		set++
	}
	if w.Memory != nil {
		set++
	}
	if set != 1 {
		return ToolArgs{}, fmt.Errorf("rca: reasoner tool_call must set exactly one payload, got %d", set)
	}
	a := ToolArgs{Tool: tool, Trace: w.Trace, Log: w.Log, Metric: w.Metric, Topology: w.Topology, Memory: w.Memory}
	if err := validateToolArgs(a); err != nil {
		return ToolArgs{}, fmt.Errorf("rca: reasoner tool_call: %w", err)
	}
	return a, nil
}

// --- DR-16/DR-20: no raw URL/host argument may reach a tool dispatch ---
//
// urlOrHostPattern is deliberately broad (defense in depth, not a precise
// parser): DR-16 §16.2's closed ToolArgs shape has no dedicated "URL" field
// anywhere — the concern is a free-text-ish field (Service, Operation,
// AttrEqual.Value, LogQueryArgs.Contains, MetricQueryArgs.Params values,
// MemoryQueryArgs.Text, ...) carrying a scheme (`http://`, `https://`,
// `ftp://`, ...), a bare `scheme://` marker, or an explicit `host:port`
// shape that a vulnerable downstream tool backend might dereference as a
// network target (DR-20's SSRF concern, extended here to the model's own
// proposed arguments per this wave's brief: "No tool argument may ever be a
// raw URL/host string"). A false positive here just means a legitimate
// value gets rejected and the loop retries/falls back — the safe failure
// direction for a security check.
var urlOrHostPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://|(?:^|[\s"'])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?::[0-9]{1,5})?(?:$|[\s"']))`)

// checkNoRawURLArgs is DR-20/DR-37's SSRF defense applied to a model-
// proposed ToolArgs: every string-typed field is scanned; the first
// URL/host-shaped value found is rejected. Called from LLMReasoner before a
// proposed call is ever returned to the Engine for dispatch.
func checkNoRawURLArgs(a ToolArgs) error {
	for _, s := range toolArgsStrings(a) {
		if urlOrHostPattern.MatchString(s) {
			return fmt.Errorf("rca: tool argument %q looks like a URL or host (SSRF defense, DR-20/DR-37)", truncateForError(s))
		}
	}
	return nil
}

func truncateForError(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func toolArgsStrings(a ToolArgs) []string {
	var out []string
	switch {
	case a.Trace != nil:
		out = append(out, a.Trace.Service, a.Trace.Operation, a.Trace.ErrorSigID)
		for _, ae := range a.Trace.AttrEquals {
			out = append(out, ae.Key, ae.Value)
		}
		out = append(out, a.Trace.Project...)
	case a.Log != nil:
		// TraceID is a fixed [16]byte array (model.TraceID), not
		// attacker-shaped free text, so it is intentionally excluded here.
		out = append(out, a.Log.Service, a.Log.Contains)
	case a.Metric != nil:
		out = append(out, a.Metric.TemplateID)
		for k, v := range a.Metric.Params {
			out = append(out, k, v)
		}
		if a.Metric.RED != nil {
			out = append(out, a.Metric.RED.Service, a.Metric.RED.Operation)
		}
	case a.Topology != nil:
		out = append(out, a.Topology.Service)
	case a.Memory != nil:
		out = append(out, a.Memory.Fingerprint, a.Memory.Text)
	}
	return out
}

// ValidateToolArgs is rca.SchemaValidator's (DR-15) second method (DR-16
// §16.3): the closed-shape check (delegated to validateToolArgs, engine.go's
// existing implementation of the same invariant), the SSRF defense above,
// and — when a TopologyReader is supplied — DR-15's Hypothesis.Component
// doc-comment rule ("MUST resolve in topology at validation time, or be
// \"\""), applied here to the tool call's own service-shaped fields rather
// than a Hypothesis, since ToolArgs carries no Component field of its own.
func (schemaValidator) ValidateToolArgs(a ToolArgs, inc model.Incident, topo TopologyReader) error {
	if err := validateToolArgs(a); err != nil {
		return err
	}
	if err := checkNoRawURLArgs(a); err != nil {
		return err
	}
	if topo == nil {
		return nil
	}
	var service string
	switch {
	case a.Trace != nil:
		service = a.Trace.Service
	case a.Log != nil:
		service = a.Log.Service
	case a.Topology != nil:
		service = a.Topology.Service
	}
	// DR-15's ValidateToolArgs signature (verbatim) carries no ctx; this
	// method's own synchronous TopologyReader.Has call therefore uses
	// context.Background() rather than threading a deadline through a
	// signature this wave must not change.
	if service != "" && !topo.Has(context.Background(), inc.Tenant, service) {
		return fmt.Errorf("rca: tool argument service %q does not resolve in topology", service)
	}
	return nil
}

var _ SchemaValidator = schemaValidator{}

// looksLikeJSON is a tiny guard used by the prompt builder to decide whether
// digest bytes are already JSON (and therefore safe to embed literally
// inside the untrusted wrapper) — kept here since it is schema-adjacent, not
// prompt-adjacent, logic.
func looksLikeJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}
