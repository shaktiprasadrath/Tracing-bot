// Package rules implements F06 §4.4's deterministic "rules" Reasoner: a
// fixed-priority-order pattern catalog, no LLM, no nondeterminism, which is
// exactly what makes DR-18's ReplayRecorded byte-reproducibility trivial for
// it (there is nothing to replay that a re-run of the same catalog against
// the same recorded tool results wouldn't reproduce).
//
// Binding source: docs/architecture/features/F06-rca-engine.md §4.4's rule
// catalog table and NextStep_rules/evaluate_rules pseudocode.
//
// Scope for this wave: 4 of the 6 catalog rules are implemented
// (error-signature-new-after-deploy, downstream-latency-propagation,
// n-plus-one-span-pattern, connection-pool-exhaustion). retry-storm and
// timeout-mismatch are deliberately deferred — same shape, same amount of
// work each, cut for time (see docs/reports/w11-rca-rules.md).
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/rca"
)

// Rule is this package's internal per-pattern contract. Not exported as part
// of rca's own interface set (DR-15 doesn't declare one) — it exists only to
// let Reasoner dispatch over a fixed-priority-order catalog uniformly.
type Rule interface {
	ID() string // also stamped onto Hypothesis.ID so NextStep can tell "already tried" from Investigation.Hypotheses alone
	Category() model.HypothesisCategory
	Statement() string
	// BuildCall returns the typed ToolArgs this rule needs to gather
	// evidence. Every rule in this wave's catalog uses exactly one tool
	// call; DR-16's Project allowlist and row shape are the rule's own
	// concern (see the row types below).
	BuildCall(inc model.Incident) rca.ToolArgs
	// Confirm evaluates a dispatched result against this rule's predicate.
	// It never errors: an unparseable/empty result is simply "not
	// confirmed" at zero confidence, exactly like the same rule seeing a
	// result with no matching rows.
	Confirm(result model.ToolResult) (confirmed bool, confidence float64, finding string)
}

// --- row types: canonical JSON projections a real store/topology backend
// would produce for this rule's tool call (DR-16 §16.2's Project allowlist,
// concretized here since the register never prints per-tool row schemas). ---

// DeployCorrelationRow backs error-signature-new-after-deploy.
type DeployCorrelationRow struct {
	ErrorSigID string    `json:"error_sig_id"`
	Service    string    `json:"service"`
	FirstSeen  time.Time `json:"first_seen"`
	DeployAt   time.Time `json:"deploy_at"`
}

// SpanTimingRow backs downstream-latency-propagation.
type SpanTimingRow struct {
	Service      string  `json:"service"`
	Operation    string  `json:"operation"`
	ChildService string  `json:"child_service"`
	SelfMillis   float64 `json:"self_millis"`
	ChildMillis  float64 `json:"child_millis"`
	TotalMillis  float64 `json:"total_millis"`
}

// SiblingSpanGroupRow backs n-plus-one-span-pattern.
type SiblingSpanGroupRow struct {
	ParentSpanID      string `json:"parent_span_id"`
	Service           string `json:"service"`
	Operation         string `json:"operation"`
	StatementTemplate string `json:"statement_template"`
	Count             int    `json:"count"`
}

// PoolMetricRow backs connection-pool-exhaustion.
type PoolMetricRow struct {
	Service              string `json:"service"`
	PoolWaitTrendUp      bool   `json:"pool_wait_trend_up"`
	ExhaustionLogMatches int    `json:"exhaustion_log_matches"`
}

// --- the four implemented rules, in F06 §4.4's priority order ---

type errorSigAfterDeploy struct{}

func (errorSigAfterDeploy) ID() string                         { return "error-signature-new-after-deploy" }
func (errorSigAfterDeploy) Category() model.HypothesisCategory { return model.CatDeployRegression }
func (errorSigAfterDeploy) Statement() string {
	return "a new error signature's first occurrence follows a deploy on the epicenter service"
}
func (errorSigAfterDeploy) BuildCall(inc model.Incident) rca.ToolArgs {
	return rca.ToolArgs{
		Tool: rca.ToolMetricQuery,
		Metric: &rca.MetricQueryArgs{
			TemplateID: "error_signature_deploy_correlation",
			Params:     map[string]string{"service": inc.EpicenterService},
		},
	}
}
func (errorSigAfterDeploy) Confirm(result model.ToolResult) (bool, float64, string) {
	rows, ok := decodeRows[DeployCorrelationRow](result.Rows)
	if !ok {
		return false, 0, "no deploy-correlation data returned"
	}
	const postDeployWindow = 30 * time.Minute
	for _, r := range rows {
		if r.FirstSeen.After(r.DeployAt) && r.FirstSeen.Sub(r.DeployAt) <= postDeployWindow {
			return true, 0.85, fmt.Sprintf("error signature %s first seen %s after deploy on %s", r.ErrorSigID, r.FirstSeen.Sub(r.DeployAt), r.Service)
		}
	}
	return false, 0, "no error signature first-seen within the post-deploy window"
}

type downstreamLatencyPropagation struct{}

func (downstreamLatencyPropagation) ID() string { return "downstream-latency-propagation" }
func (downstreamLatencyPropagation) Category() model.HypothesisCategory {
	return model.CatDependencyFailure
}
func (downstreamLatencyPropagation) Statement() string {
	return "elevated duration is explained by a downstream child span, not local self-time"
}
func (downstreamLatencyPropagation) BuildCall(inc model.Incident) rca.ToolArgs {
	return rca.ToolArgs{
		Tool: rca.ToolTraceQuery,
		Trace: &rca.TraceQueryArgs{
			Service: inc.EpicenterService,
			Status:  model.StatusFilterAny,
			Limit:   50,
		},
	}
}
func (downstreamLatencyPropagation) Confirm(result model.ToolResult) (bool, float64, string) {
	rows, ok := decodeRows[SpanTimingRow](result.Rows)
	if !ok {
		return false, 0, "no span timing data returned"
	}
	const childRatioThreshold = 0.8
	for _, r := range rows {
		if r.TotalMillis <= 0 {
			continue
		}
		ratio := r.ChildMillis / r.TotalMillis
		if ratio >= childRatioThreshold {
			return true, 0.8, fmt.Sprintf("%s.%s: downstream %s accounts for %.0f%% of total duration", r.Service, r.Operation, r.ChildService, ratio*100)
		}
	}
	return false, 0, "no span had a downstream child accounting for >=80% of total duration"
}

type nPlusOneSpanPattern struct{ threshold int }

func (nPlusOneSpanPattern) ID() string                         { return "n-plus-one-span-pattern" }
func (nPlusOneSpanPattern) Category() model.HypothesisCategory { return model.CatDataSkew }
func (nPlusOneSpanPattern) Statement() string {
	return "many sibling spans share an identical (service, operation, statement template) under one parent"
}
func (r nPlusOneSpanPattern) BuildCall(inc model.Incident) rca.ToolArgs {
	return rca.ToolArgs{
		Tool: rca.ToolTraceQuery,
		Trace: &rca.TraceQueryArgs{
			Service: inc.EpicenterService,
			Status:  model.StatusFilterAny,
			Limit:   50,
		},
	}
}
func (r nPlusOneSpanPattern) Confirm(result model.ToolResult) (bool, float64, string) {
	rows, ok := decodeRows[SiblingSpanGroupRow](result.Rows)
	if !ok {
		return false, 0, "no sibling-span grouping data returned"
	}
	threshold := r.threshold
	if threshold <= 0 {
		threshold = 10 // F06 §4.4's default N
	}
	for _, g := range rows {
		if g.Count >= threshold {
			conf := 0.7 + 0.02*float64(g.Count-threshold)
			if conf > 0.95 {
				conf = 0.95
			}
			return true, conf, fmt.Sprintf("%d sibling spans of %s.%s (%s) under parent %s", g.Count, g.Service, g.Operation, g.StatementTemplate, g.ParentSpanID)
		}
	}
	return false, 0, "no sibling-span group reached the N+1 threshold"
}

type connectionPoolExhaustion struct{}

func (connectionPoolExhaustion) ID() string { return "connection-pool-exhaustion" }
func (connectionPoolExhaustion) Category() model.HypothesisCategory {
	return model.CatResourceExhaustion
}
func (connectionPoolExhaustion) Statement() string {
	return "pool-wait time is rising concurrently with pool-exhaustion log signatures"
}
func (connectionPoolExhaustion) BuildCall(inc model.Incident) rca.ToolArgs {
	return rca.ToolArgs{
		Tool: rca.ToolMetricQuery,
		Metric: &rca.MetricQueryArgs{
			TemplateID: "pool_wait_and_exhaustion_logs",
			Params:     map[string]string{"service": inc.EpicenterService},
		},
	}
}
func (connectionPoolExhaustion) Confirm(result model.ToolResult) (bool, float64, string) {
	rows, ok := decodeRows[PoolMetricRow](result.Rows)
	if !ok {
		return false, 0, "no pool metric data returned"
	}
	for _, r := range rows {
		if r.PoolWaitTrendUp && r.ExhaustionLogMatches > 0 {
			return true, 0.8, fmt.Sprintf("%s: pool wait trending up with %d exhaustion log matches", r.Service, r.ExhaustionLogMatches)
		}
	}
	return false, 0, "no concurrent pool-wait rise and exhaustion log signature"
}

func decodeRows[T any](raw []byte) ([]T, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var rows []T
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, false
	}
	return rows, true
}

// catalog is F06 §4.4's fixed priority order, restricted to this wave's
// four implemented rules.
var catalog = []Rule{
	errorSigAfterDeploy{},
	downstreamLatencyPropagation{},
	nPlusOneSpanPattern{threshold: 10},
	connectionPoolExhaustion{},
}

// Reasoner is the deterministic rca.Reasoner (DR-15's Kind() == "rules").
// It holds no mutable state across calls: "already tried" is derived purely
// from state.Investigation.Hypotheses (each rule stamps its own ID there),
// which is what makes it replay-safe and testable without a live instance
// per investigation.
type Reasoner struct{}

func New() Reasoner { return Reasoner{} }

func (Reasoner) Kind() model.ReasonerKind { return model.ReasonerRules }

func (Reasoner) NextStep(_ context.Context, _ model.TenantID, s rca.State) (rca.Proposal, error) {
	// Fold in the previous dispatch's result, if any (see rca.State's doc
	// comment on LastResult/LastHypothesisID for why this can't be a
	// separate Reasoner method).
	if s.LastResult != nil && s.LastHypothesisID != "" {
		if rule, ok := ruleByID(s.LastHypothesisID); ok {
			confirmed, conf, finding := rule.Confirm(*s.LastResult)
			status := model.HypothesisRefuted
			if confirmed {
				status = model.HypothesisSupported
			}
			done := confirmed && conf >= confidenceThresholdFor(s)
			return rca.Proposal{
				Phase: model.PhaseValidate,
				Hypotheses: []model.Hypothesis{{
					ID:        rule.ID(),
					Statement: finding,
					Category:  rule.Category(),
					Component: s.Incident.EpicenterService,
					PostScore: conf,
					Status:    status,
					Source:    model.HypSourceRule,
				}},
				Done:      done,
				Rationale: finding,
			}, nil
		}
	}

	tried := triedRuleIDs(s.Investigation)
	for _, rule := range catalog {
		if tried[rule.ID()] {
			continue
		}
		call := rule.BuildCall(s.Incident)
		return rca.Proposal{
			Phase: model.PhaseTest,
			Hypotheses: []model.Hypothesis{{
				ID:        rule.ID(),
				Statement: rule.Statement(),
				Category:  rule.Category(),
				Component: s.Incident.EpicenterService,
				Status:    model.HypothesisTesting,
				Source:    model.HypSourceRule,
			}},
			Call:      &call,
			Rationale: "testing " + rule.ID(),
		}, nil
	}
	return rca.Proposal{}, rca.ErrNoMoreRules
}

// confidenceThresholdFor lets a per-investigation ConfidenceCap (DR-34
// §34.4's fallback-swap rule; not exercised by the rules reasoner itself
// since it never swaps further, but the hook costs nothing) override the
// package default.
func confidenceThresholdFor(_ rca.State) float64 { return 0.75 }

func (Reasoner) Conclude(_ context.Context, _ model.TenantID, s rca.State) (model.Conclusion, error) {
	var best *model.Hypothesis
	hyps := s.Investigation.Hypotheses
	for i := range hyps {
		h := &hyps[i]
		if h.Status != model.HypothesisSupported {
			continue
		}
		if best == nil || h.PostScore > best.PostScore {
			best = h
		}
	}
	if best == nil {
		return model.Conclusion{
			Hypotheses: hyps,
			Rationale:  "no rule confirmed a hypothesis within budget",
		}, nil
	}
	return model.Conclusion{
		RootCause:  best.Statement,
		Confidence: best.PostScore,
		Hypotheses: hyps,
		Rationale:  fmt.Sprintf("rule %s confirmed at confidence %.2f", best.ID, best.PostScore),
	}, nil
}

func ruleByID(id string) (Rule, bool) {
	for _, r := range catalog {
		if r.ID() == id {
			return r, true
		}
	}
	return nil, false
}

func triedRuleIDs(inv model.Investigation) map[string]bool {
	out := make(map[string]bool, len(inv.Hypotheses))
	for _, h := range inv.Hypotheses {
		out[h.ID] = true
	}
	return out
}

var _ rca.Reasoner = Reasoner{}
