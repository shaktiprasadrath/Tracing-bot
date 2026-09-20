package nl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/auth"
	"traceiq/internal/model"
	"traceiq/internal/rca"
)

// capability strings referenced by F10 §4.4's pseudocode ("require
// capability incident:investigate" / "remediation_action:propose"). Not
// promoted to auth-package constants (out of nl's edit scope); the literal
// strings are DR-25 §25.2's matrix values, cited verbatim.
const (
	capIncidentInvestigate = auth.Capability("incident:investigate")
	capRemediatePropose    = auth.Capability("remediation_action:propose")
)

// RemediateProposal is the minimal, typed payload Answerer hands to
// RemediateProposer — never a free-form map (DR-16 §16.2's no-map-string-any
// discipline extended to the F09 boundary). Raw is retained for the audit
// row only, never re-parsed (DR-37).
type RemediateProposal struct {
	TenantID model.TenantID
	Service  string
	Raw      string
}

// RemediateProposer is a consumer-declared backend (DR-2's signature rule):
// internal/remediate is NOT in doc.go's allowed-import list for internal/nl,
// so this interface is satisfied structurally by remediate.Guard at the
// wiring layer (cmd/traceiq) without nl importing remediate — the same
// pattern rca uses for TraceStore/LogStore/etc. Propose ONLY: there is
// deliberately no Execute reachable from nl (FR-F10-6) — nl is a proposer,
// never an executor.
type RemediateProposer interface {
	Propose(ctx context.Context, tid model.TenantID, p RemediateProposal, subject auth.Subject) (actionID string, err error)
}

// Authorizer is a consumer-declared, single-method narrowing of
// auth.Authorizer (DR-2's signature rule, same pattern as rca.TopologyReader
// narrowing topology.Graph to Has()): RulesAnswerer needs only the RBAC
// capability check, not SeparationOfDuty/Roles, so it declares just that
// method here — satisfied structurally by the real auth.Authorizer without
// nl depending on its full surface.
type Authorizer interface {
	Can(ctx context.Context, s auth.Subject, c auth.Capability, res auth.ResourceRef) error
}

// RulesAnswerer is DR-35 §35.2/F10 §4.4's Answerer. Every data access goes
// through rca.ToolRegistry.Dispatch (FR-F10-10) EXCEPT the three kinds
// DR-35 §35.4's pseudocode calls out by name: IntentIncidentStatus
// (anomaly.Grouper.ActiveIncidents), IntentStartInvestigation
// (rca.Engine.Investigate) and IntentRemediate (RemediateProposer.Propose).
type RulesAnswerer struct {
	Registry  rca.ToolRegistry
	Engine    rca.Engine
	Grouper   anomaly.Grouper
	Remediate RemediateProposer
	AuthZ     Authorizer
	Clock     model.Clock
}

func (a *RulesAnswerer) now() time.Time {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now()
}

// Answer implements the nl.Answerer interface exactly as nl.go declares it
// (Answer(ctx, tid, in, cc) — no auth.Subject parameter). FR-F10-6/§4.4
// require the chat user's resolved auth.Subject to gate IntentRemediate and
// IntentStartInvestigation; since that Subject cannot travel through this
// fixed signature, Answer is safe-by-default here — it builds a
// zero-privilege Subject (no Roles), so both capability-gated intents
// always refuse via this path rather than silently skipping the check.
// Callers that HAVE the resolved Subject (the API gateway, per FR-F10-7's
// identity-resolution step) MUST call AnswerForSubject directly.
// TODO(DR-35): the register's own Answerer interface never prints an
// auth.Subject parameter despite requiring one for FR-F10-6 — flagged here
// rather than silently widened, since changing the interface would ripple
// into internal/api (out of this package's edit scope).
func (a *RulesAnswerer) Answer(ctx context.Context, tid model.TenantID, in Intent, cc ConversationContext) (Answer, error) {
	return a.AnswerForSubject(ctx, tid, in, cc, auth.Subject{Tenant: tid})
}

// AnswerForSubject is the capability-aware entry point used once a caller
// has a resolved auth.Subject (FR-F10-7). tid is the SERVER-RESOLVED tenant
// (from auth.Subject.Tenant / tenant.Resolver, DR-5) — it is used for every
// dispatch below in preference to in.Args.TenantID, which a crafted or
// stale Intent could otherwise carry, so a wrong/attacker Args.TenantID can
// never leak another tenant's evidence into this Answer.
func (a *RulesAnswerer) AnswerForSubject(ctx context.Context, tid model.TenantID, in Intent, cc ConversationContext, subject auth.Subject) (Answer, error) {
	if in.Kind == IntentUnknown {
		return renderClarifying(in), nil
	}

	var (
		ans Answer
		err error
	)
	switch in.Kind {
	case IntentIncidentStatus:
		ans, err = a.answerIncidentStatus(ctx, tid)
	case IntentStartInvestigation:
		ans, err = a.answerStartInvestigation(ctx, tid, in, subject)
	case IntentRemediate:
		ans, err = a.answerRemediate(ctx, tid, in, subject)
	case IntentCompareWindows:
		ans, err = a.answerCompareWindows(ctx, tid, in)
	default:
		ans, err = a.answerViaRegistry(ctx, tid, in)
	}
	if err == nil && !ans.Clarifying {
		ans.Text = appendFuzzyServiceCaveat(ans.Text, in)
	}
	return ans, err
}

// appendFuzzyServiceCaveat implements F10 §5's documented mitigation for the
// fuzzy-service-match false-positive risk (W14 review #6): "Answer includes
// the matched service name inline with a 'did you mean payment-svc?' caveat
// rather than silently assuming intent." in.FuzzyServiceMatch is set by
// extractToolArgs only when THIS turn's text matched the live catalog at a
// non-exact (Levenshtein distance > 0) distance and that match was actually
// used to build Args — never for an exact match, and never merely because a
// fuzzy candidate existed somewhere in the text.
//
// Skipped on Clarifying answers (permission refusals, disambiguation
// questions): those never mention a matched service, and appending a caveat
// there would leak a guessed service name past an authorization failure or
// a question the user hasn't actually resolved.
func appendFuzzyServiceCaveat(text string, in Intent) string {
	if in.FuzzyServiceMatch == "" || text == "" {
		return text
	}
	return text + fmt.Sprintf(" (matched fuzzily to %q — did you mean this service?)", in.FuzzyServiceMatch)
}

func renderClarifying(in Intent) Answer {
	text := "I'm not sure what you're asking."
	if len(in.Candidates) > 0 {
		text = "I could interpret that a couple of ways — could you clarify which you mean?"
	}
	return Answer{
		Text:       text,
		Clarifying: true,
		IntentKind: IntentUnknown,
		FollowUps:  candidateFollowUps(in.Candidates),
	}
}

func candidateFollowUps(cands []IntentKind) []string {
	var out []string
	for _, c := range cands {
		out = append(out, intentDescription(c))
	}
	return out
}

func intentDescription(k IntentKind) string {
	switch k {
	case IntentTraceSearch:
		return "search for traces"
	case IntentServiceHealth:
		return "check a service's health"
	case IntentTopologyQuestion:
		return "ask about service topology"
	case IntentIncidentStatus:
		return "check active incident status"
	case IntentInvestigationAsk:
		return "ask about an existing investigation"
	case IntentMemoryLookup:
		return "look up similar past incidents"
	case IntentStartInvestigation:
		return "start a new investigation"
	case IntentCompareWindows:
		return "compare two time windows"
	case IntentExplainTrace:
		return "explain a specific trace"
	case IntentRemediate:
		return "propose a remediation action"
	default:
		return "unknown"
	}
}

// answerViaRegistry is the single generic dispatch path for every intent
// that queries through rca.ToolRegistry.Dispatch (FR-F10-10): TraceSearch,
// ServiceHealth, TopologyQuestion, ExplainTrace, MemoryLookup,
// InvestigationAsk.
func (a *RulesAnswerer) answerViaRegistry(ctx context.Context, tid model.TenantID, in Intent) (Answer, error) {
	if a.Registry == nil {
		return Answer{Text: "Data lookup is unavailable right now.", IntentKind: in.Kind}, nil
	}
	args := in.Args
	args.TenantID = tid // DR-5: server-resolved tenant always wins over Args.TenantID

	result, err := a.Registry.Dispatch(ctx, tid, args)
	if err != nil {
		return Answer{Text: "I couldn't complete that query: " + err.Error(), IntentKind: in.Kind}, nil
	}
	ev := evidenceFromResult(args, result, a.now())
	return Answer{
		Text:       renderAnswerText(in.Kind, args, result),
		Evidence:   []model.Evidence{ev},
		Citations:  []string{ev.ID},
		IntentKind: in.Kind,
	}, nil
}

// answerCompareWindows issues two Dispatch calls — the requested window and
// an equal-length preceding window — through the SAME rca.ToolRegistry path
// (FR-F10-10); no second query mechanism.
func (a *RulesAnswerer) answerCompareWindows(ctx context.Context, tid model.TenantID, in Intent) (Answer, error) {
	if a.Registry == nil || in.Args.Metric == nil {
		return Answer{Text: "Comparison is unavailable right now.", IntentKind: in.Kind}, nil
	}
	curArgs := in.Args
	curArgs.TenantID = tid
	curResult, err := a.Registry.Dispatch(ctx, tid, curArgs)
	if err != nil {
		return Answer{Text: "I couldn't complete that comparison: " + err.Error(), IntentKind: in.Kind}, nil
	}

	dur := in.Args.Metric.End.Sub(in.Args.Metric.Start)
	prevMetric := *in.Args.Metric
	prevMetric.End = in.Args.Metric.Start
	prevMetric.Start = in.Args.Metric.Start.Add(-dur)
	prevArgs := in.Args
	prevArgs.TenantID = tid
	prevArgs.Metric = &prevMetric
	prevResult, err := a.Registry.Dispatch(ctx, tid, prevArgs)
	if err != nil {
		return Answer{Text: "I couldn't complete that comparison: " + err.Error(), IntentKind: in.Kind}, nil
	}

	evA := evidenceFromResult(curArgs, curResult, a.now())
	evB := evidenceFromResult(prevArgs, prevResult, a.now())
	return Answer{
		Text:       fmt.Sprintf("Comparing %s to the prior equal-length window for %s.", in.Args.Metric.Start.Format(time.RFC3339), in.Args.Metric.RED.Service),
		Evidence:   []model.Evidence{evA, evB},
		Citations:  []string{evA.ID, evB.ID},
		IntentKind: in.Kind,
	}, nil
}

func (a *RulesAnswerer) answerIncidentStatus(ctx context.Context, tid model.TenantID) (Answer, error) {
	if a.Grouper == nil {
		return Answer{Text: "Incident status is unavailable right now.", IntentKind: IntentIncidentStatus}, nil
	}
	incidents, err := a.Grouper.ActiveIncidents(ctx, tid)
	if err != nil {
		return Answer{Text: "I couldn't fetch incident status: " + err.Error(), IntentKind: IntentIncidentStatus}, nil
	}
	if len(incidents) == 0 {
		return Answer{Text: "No active incidents.", IntentKind: IntentIncidentStatus}, nil
	}
	text := fmt.Sprintf("%d active incident(s):", len(incidents))
	var evs []model.Evidence
	var cites []string
	for _, inc := range incidents {
		text += fmt.Sprintf("\n- %s (%s), severity %d", inc.Title, inc.ID, inc.Severity)
		evs = append(evs, model.Evidence{
			ID:         inc.ID,
			Category:   model.EvTopologyEdge,
			Source:     "anomaly.Grouper",
			Summary:    inc.Title,
			ObservedAt: inc.UpdatedAt,
			Weight:     inc.Score,
		})
		cites = append(cites, inc.ID)
	}
	return Answer{Text: text, Evidence: evs, Citations: cites, IntentKind: IntentIncidentStatus}, nil
}

func (a *RulesAnswerer) answerStartInvestigation(ctx context.Context, tid model.TenantID, in Intent, subject auth.Subject) (Answer, error) {
	// FR-F10-6/FR-F10-7: this AuthZ.Can check is the ONLY capability gate on
	// this path — rca.Engine.Investigate has no independent RBAC check of its
	// own (it trusts its caller). An unconfigured (nil) AuthZ must therefore
	// FAIL CLOSED, not silently skip the check: a wiring omission must never
	// turn into "anyone can start an investigation."
	if a.AuthZ == nil {
		return Answer{Text: "You don't have permission to start an investigation.", Clarifying: true, IntentKind: IntentStartInvestigation}, nil
	}
	if err := a.AuthZ.Can(ctx, subject, capIncidentInvestigate, auth.ResourceRef{Tenant: tid}); err != nil {
		return Answer{Text: "You don't have permission to start an investigation.", Clarifying: true, IntentKind: IntentStartInvestigation}, nil
	}
	if a.Engine == nil {
		return Answer{Text: "The investigation engine is unavailable right now.", IntentKind: IntentStartInvestigation}, nil
	}
	service := ""
	if in.Args.Trace != nil {
		service = in.Args.Trace.Service
	}
	inc := model.Incident{
		Tenant:    tid,
		Title:     fmt.Sprintf("NL-triggered investigation: %s", in.Raw),
		Services:  nonEmptyServices(service),
		CreatedAt: a.now(),
		UpdatedAt: a.now(),
	}
	investigation, err := a.Engine.Investigate(ctx, tid, inc)
	if err != nil {
		return Answer{Text: "I couldn't start an investigation: " + err.Error(), IntentKind: IntentStartInvestigation}, nil
	}
	ev := model.Evidence{
		ID:              investigation.ID,
		InvestigationID: investigation.ID,
		Source:          "rca.Engine",
		Summary:         "Investigation started from chat",
		ObservedAt:      a.now(),
	}
	return Answer{
		Text:       fmt.Sprintf("Started investigation %s for %s.", investigation.ID, service),
		Evidence:   []model.Evidence{ev},
		Citations:  []string{investigation.ID},
		IntentKind: IntentStartInvestigation,
	}, nil
}

func nonEmptyServices(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// answerRemediate never dispatches through rca.ToolRegistry and never
// executes anything — it only ever calls RemediateProposer.Propose after an
// explicit capability check, per FR-F10-6/AC-F10-6.
func (a *RulesAnswerer) answerRemediate(ctx context.Context, tid model.TenantID, in Intent, subject auth.Subject) (Answer, error) {
	// FR-F10-6/AC-F10-6: this AuthZ.Can check is the ONLY capability gate on
	// this path — remediate.Guard.Propose does not itself re-check
	// "remediation_action:propose" (its Authz field is used only for
	// SeparationOfDuty at Approve time), so nl is the sole enforcement point.
	// An unconfigured (nil) AuthZ must FAIL CLOSED rather than silently
	// letting every proposal through.
	if a.AuthZ == nil {
		return Answer{Text: "You don't have permission to propose a remediation action.", Clarifying: true, IntentKind: IntentRemediate}, nil
	}
	if err := a.AuthZ.Can(ctx, subject, capRemediatePropose, auth.ResourceRef{Tenant: tid}); err != nil {
		return Answer{Text: "You don't have permission to propose a remediation action.", Clarifying: true, IntentKind: IntentRemediate}, nil
	}
	if a.Remediate == nil {
		return Answer{Text: "Remediation proposals are unavailable right now.", IntentKind: IntentRemediate}, nil
	}
	service := ""
	if in.Args.Trace != nil {
		service = in.Args.Trace.Service
	}
	proposal := RemediateProposal{TenantID: tid, Service: service, Raw: in.Raw}
	actionID, err := a.Remediate.Propose(ctx, tid, proposal, subject)
	if err != nil {
		return Answer{Text: "I couldn't create that remediation proposal: " + err.Error(), IntentKind: IntentRemediate}, nil
	}
	return Answer{
		Text:       fmt.Sprintf("Proposed remediation action %s for %s. It requires a separate approval before it runs.", actionID, service),
		Citations:  []string{actionID},
		IntentKind: IntentRemediate,
	}, nil
}

// --- evidence/citation rendering ---

// evidenceFromResult builds a model.Evidence whose PayloadJSON is EXACTLY
// the bytes rca.ToolRegistry.Dispatch returned — never a fabricated or
// re-derived string — so every citation is real (FR-F10-5). ID is a sha256
// hash of those same bytes, using the identical "sha256:"+hex construction
// rca.Step.ToolResultHash uses (internal/rca/journal.go's sha256Hex), so an
// NL answer and an RCA step dispatched over the same Args/Rows converge on
// the same hash (AC-F10-10's spirit) without nl reaching into rca's
// unexported helper.
func evidenceFromResult(args rca.ToolArgs, result model.ToolResult, now time.Time) model.Evidence {
	sum := sha256.Sum256(result.Rows)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	return model.Evidence{
		ID:          hash,
		Category:    categoryForTool(args.Tool),
		Source:      "rca.ToolRegistry",
		Query:       summarizeArgs(args),
		Ref:         hash,
		Summary:     summarizeArgs(args),
		PayloadJSON: result.Rows,
		Weight:      1,
		ObservedAt:  firstNonZero(result.ObservedAt, now),
	}
}

func firstNonZero(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func categoryForTool(tool model.ToolName) model.EvidenceCategory {
	switch tool {
	case rca.ToolTraceQuery:
		return model.EvTraceExemplar
	case rca.ToolLogQuery:
		return model.EvLogLine
	case rca.ToolMetricQuery:
		return model.EvMetricSeries
	case rca.ToolTopologyQuery:
		return model.EvTopologyEdge
	case rca.ToolMemoryQuery:
		return model.EvMemoryRecord
	default:
		return 0
	}
}

// summarizeArgs renders a deterministic, replayable query description —
// "the exact executed query" model.Evidence.Query documents — built only
// from the typed ToolArgs fields, never free text.
func summarizeArgs(args rca.ToolArgs) string {
	switch args.Tool {
	case rca.ToolTraceQuery:
		if args.Trace != nil {
			return fmt.Sprintf("trace_query(service=%s, start=%s, end=%s)", args.Trace.Service, args.Trace.Start.Format(time.RFC3339), args.Trace.End.Format(time.RFC3339))
		}
	case rca.ToolMetricQuery:
		if args.Metric != nil && args.Metric.RED != nil {
			return fmt.Sprintf("metric_query(red.service=%s, start=%s, end=%s)", args.Metric.RED.Service, args.Metric.Start.Format(time.RFC3339), args.Metric.End.Format(time.RFC3339))
		}
	case rca.ToolTopologyQuery:
		if args.Topology != nil {
			return fmt.Sprintf("topology_query(service=%s, hops=%d)", args.Topology.Service, args.Topology.Hops)
		}
	case rca.ToolMemoryQuery:
		if args.Memory != nil {
			return fmt.Sprintf("memory_query(text=%s)", args.Memory.Text)
		}
	case rca.ToolLogQuery:
		if args.Log != nil {
			return fmt.Sprintf("log_query(service=%s)", args.Log.Service)
		}
	}
	return string(args.Tool)
}

// renderAnswerText builds a minimal, evidence-anchored answer body. It
// never states a root cause or metric value beyond what result.Rows itself
// carries (FR-F10-5) — the numeric content stays inside Evidence/Citations,
// not asserted as prose here.
func renderAnswerText(kind IntentKind, args rca.ToolArgs, result model.ToolResult) string {
	base := summarizeArgs(args)
	switch kind {
	case IntentTraceSearch:
		return "Traces matching your query: " + base
	case IntentExplainTrace:
		return "Trace details: " + base
	case IntentServiceHealth:
		return "Health data: " + base
	case IntentTopologyQuestion:
		return "Topology data: " + base
	case IntentMemoryLookup:
		return "Related past records: " + base
	case IntentInvestigationAsk:
		return "Related investigation records: " + base
	default:
		return base
	}
}
