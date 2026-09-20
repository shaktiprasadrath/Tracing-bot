package nl

import (
	"context"
	"regexp"
	"sort"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/topology"
)

// Rule is DR-35 §35.4's pattern-table row.
type Rule struct {
	Intent   IntentKind
	Pattern  *regexp.Regexp
	Priority int
}

// ServiceCatalogFunc resolves the live topology.Graph service-catalog
// snapshot for a tenant (FR-F10-2). Declared as a func type, rather than an
// interface, so RulesInterpreter stays decoupled from topology.Graph's full
// surface — the caller typically wires topology.Graph.Snapshot.
type ServiceCatalogFunc func(ctx context.Context, tid model.TenantID) ([]string, error)

// DefaultAmbiguityThreshold is nl.ambiguity_threshold's default (F10 §4.3).
const DefaultAmbiguityThreshold = 0.15

// RulesInterpreter is DR-35 §35.4/F10 §4.3's mandatory, always-available
// offline path. It resolves all ten IntentKind values from a deterministic
// pattern table with no network call; nl.interpreter: rules is a fully
// supported configuration, never a degraded one.
type RulesInterpreter struct {
	Patterns           []Rule
	ServiceCatalog     ServiceCatalogFunc
	Clock              model.Clock
	AmbiguityThreshold float64 // defaults to DefaultAmbiguityThreshold when zero

	// Deploys resolves deploy-marker time phrases ("since the checkout-svc
	// deploy", FR-F10-3/F10 §4) via anomaly.DeployIndex (DR-14 §14.6). Left
	// nil, such phrases fall through to extractTimeRange's own ok=false
	// handling rather than resolving — a supported, non-degraded
	// configuration (see resolveDeployTimeRange).
	Deploys anomaly.DeployIndex
}

// NewRulesInterpreter builds a RulesInterpreter with the built-in pattern
// table (DefaultPatterns, >= 4 surface forms per intent, FR-F10-9).
func NewRulesInterpreter(catalog ServiceCatalogFunc, clock model.Clock) *RulesInterpreter {
	return &RulesInterpreter{
		Patterns:       DefaultPatterns(),
		ServiceCatalog: catalog,
		Clock:          clock,
	}
}

func (r *RulesInterpreter) Kind() string { return "rules" }

func (r *RulesInterpreter) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now()
}

func (r *RulesInterpreter) ambiguityThreshold() float64 {
	if r.AmbiguityThreshold > 0 {
		return r.AmbiguityThreshold
	}
	return DefaultAmbiguityThreshold
}

// staticConfidence derives a deterministic confidence score from a rule's
// Priority (higher priority => higher confidence), capped at 0.95.
func staticConfidence(priority int) float64 {
	c := 0.5 + 0.05*float64(priority)
	if c > 0.95 {
		c = 0.95
	}
	if c < 0.5 {
		c = 0.5
	}
	return c
}

// Interpret implements Interpreter per DR-35 §35.4's pseudocode: match
// candidates, extract typed entities via extractToolArgs, and — when the
// top two candidates are within AmbiguityThreshold of each other — return
// IntentUnknown with both candidates rather than guess (FR-F10-11).
func (r *RulesInterpreter) Interpret(ctx context.Context, tid model.TenantID, q Question, cc ConversationContext) (Intent, error) {
	text := q.Text

	best := map[IntentKind]float64{}
	for _, rule := range r.Patterns {
		if rule.Pattern == nil {
			continue
		}
		if rule.Pattern.MatchString(text) {
			c := staticConfidence(rule.Priority)
			if c > best[rule.Intent] {
				best[rule.Intent] = c
			}
		}
	}

	if len(best) == 0 {
		return Intent{Kind: IntentUnknown, Raw: text, Source: r.Kind(), Args: rca.ToolArgs{TenantID: tid}}, nil
	}

	type kc struct {
		kind IntentKind
		conf float64
	}
	ranked := make([]kc, 0, len(best))
	for k, c := range best {
		ranked = append(ranked, kc{k, c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].conf != ranked[j].conf {
			return ranked[i].conf > ranked[j].conf
		}
		return ranked[i].kind < ranked[j].kind // deterministic tiebreak
	})

	var catalog []string
	if r.ServiceCatalog != nil {
		if c, err := r.ServiceCatalog(ctx, tid); err == nil {
			catalog = c
		}
	}

	args, fuzzyService := extractToolArgs(ctx, text, tid, ranked[0].kind, catalog, r.Deploys, cc.Focus, r.now())
	args = mergeFollowUpArgs(args, ranked[0].kind, cc)

	if len(ranked) > 1 && (ranked[0].conf-ranked[1].conf) < r.ambiguityThreshold() {
		return Intent{
			Kind:              IntentUnknown,
			Candidates:        []IntentKind{ranked[0].kind, ranked[1].kind},
			Args:              args,
			Raw:               text,
			Source:            r.Kind(),
			FuzzyServiceMatch: fuzzyService,
		}, nil
	}

	return Intent{
		Kind:              ranked[0].kind,
		Args:              args,
		Raw:               text,
		Confidence:        ranked[0].conf,
		Source:            r.Kind(),
		FuzzyServiceMatch: fuzzyService,
	}, nil
}

// extractToolArgs is DR-35 §35.4's extractToolArgs: it populates the typed
// rca.ToolArgs field the matched intent implies — never a free-form string
// field or map[string]any (DR-16 §16.2).
//
// It also returns the fuzzy-service-match caveat name (W14 review #6, F10
// §5): non-empty only when THIS turn's text matched the catalog at a
// non-exact distance AND the switch below actually placed that matched
// service into args — never when service instead came from Focus carryover
// (that's a remembered fact, not a guess) and never for a branch (like
// IntentIncidentStatus, or IntentMemoryLookup/IntentInvestigationAsk when
// their own preferred field is non-empty) that ends up not using it.
func extractToolArgs(ctx context.Context, text string, tid model.TenantID, kind IntentKind, catalog []string, deploys anomaly.DeployIndex, focus Focus, now time.Time) (rca.ToolArgs, string) {
	args := rca.ToolArgs{TenantID: tid}

	matched, distance, hasService := matchService(text, catalog)
	service := matched
	if !hasService {
		service = focus.Service
	}
	fuzzyMatch := ""
	markFuzzy := func(used bool) {
		if used && hasService && distance > 0 {
			fuzzyMatch = matched
		}
	}
	start, end, hasTime := extractTimeRange(text, now)
	if !hasTime {
		start, end, hasTime = resolveDeployTimeRange(ctx, tid, text, catalog, deploys, now)
	}
	if !hasTime {
		// default to a trailing 30-minute window so downstream tools never
		// receive a zero-value window (Answerer still cites the actual
		// evidence it gets back; this only bounds the query, never fabricates
		// a result).
		end = now
		start = now.Add(-30 * time.Minute)
	}
	traceID, hasTraceID := extractTraceID(text)
	errStr, hasErr := extractErrorString(text)

	switch kind {
	case IntentTraceSearch:
		args.Tool = rca.ToolTraceQuery
		ta := &rca.TraceQueryArgs{Service: service, Start: start, End: end, Limit: 50}
		if hasErr {
			ta.Status = model.StatusFilterError
			ta.ErrorSigID = errStr
		}
		args.Trace = ta
		markFuzzy(true)

	case IntentExplainTrace:
		args.Tool = rca.ToolTraceQuery
		ta := &rca.TraceQueryArgs{Service: service, Start: start, End: end, Limit: 1}
		if !hasTraceID && focus.TraceID != "" {
			ta.AttrEquals = []rca.AttrEqual{{Key: "trace_id", Value: focus.TraceID}}
		} else if hasTraceID {
			ta.AttrEquals = []rca.AttrEqual{{Key: "trace_id", Value: traceIDHex(traceID)}}
		}
		args.Trace = ta
		markFuzzy(true)

	case IntentServiceHealth:
		args.Tool = rca.ToolMetricQuery
		args.Metric = &rca.MetricQueryArgs{
			RED:   &rca.REDQuery{Service: service},
			Start: start,
			End:   end,
		}
		markFuzzy(true)

	case IntentTopologyQuestion:
		args.Tool = rca.ToolTopologyQuery
		args.Topology = &rca.TopologyQueryArgs{
			Service:   service,
			Hops:      1,
			Direction: topology.Both,
			Start:     start,
			End:       end,
			Limit:     100,
		}
		markFuzzy(true)

	case IntentCompareWindows:
		args.Tool = rca.ToolMetricQuery
		args.Metric = &rca.MetricQueryArgs{
			RED:   &rca.REDQuery{Service: service},
			Start: start,
			End:   end,
		}
		markFuzzy(true)

	case IntentMemoryLookup:
		args.Tool = rca.ToolMemoryQuery
		text := errStr
		usedService := text == ""
		if usedService {
			text = service
		}
		args.Memory = &rca.MemoryQueryArgs{Text: text, TopK: 8}
		markFuzzy(usedService)

	case IntentInvestigationAsk:
		args.Tool = rca.ToolMemoryQuery
		q := focus.InvestigationID
		usedService := q == ""
		if usedService {
			q = service
		}
		args.Memory = &rca.MemoryQueryArgs{Text: q, TopK: 8}
		markFuzzy(usedService)

	case IntentIncidentStatus:
		// No entities needed; dispatched via anomaly.Grouper.ActiveIncidents,
		// never rca.ToolRegistry (DR-35 §35.4) — Args.Tool stays "".

	case IntentStartInvestigation, IntentRemediate:
		// Args.Tool stays "" (dispatched via rca.Engine.Investigate /
		// RemediateProposer.Propose, never rca.ToolRegistry, per DR-35
		// §35.4/FR-F10-6) but Trace carries the extracted service/window so
		// Answerer can build the incident/proposal without a second
		// entity-extraction pass over the raw text.
		args.Trace = &rca.TraceQueryArgs{Service: service, Start: start, End: end}
		markFuzzy(true)
	}

	return args, fuzzyMatch
}

// mergeFollowUpArgs carries forward missing fields from the last turn in
// cc.Turns, keyed per THREAD (cc.Key), never per channel (FR-F10-8). It
// only fills genuinely empty fields — it never overwrites an entity this
// turn's text actually extracted.
func mergeFollowUpArgs(args rca.ToolArgs, kind IntentKind, cc ConversationContext) rca.ToolArgs {
	if len(cc.Turns) == 0 {
		return args
	}
	last := cc.Turns[len(cc.Turns)-1]
	if last.Intent.Kind != kind {
		return args
	}
	switch kind {
	case IntentTraceSearch, IntentExplainTrace:
		if args.Trace != nil && last.Intent.Args.Trace != nil {
			if args.Trace.Service == "" {
				args.Trace.Service = last.Intent.Args.Trace.Service
			}
		}
	case IntentServiceHealth, IntentCompareWindows:
		if args.Metric != nil && args.Metric.RED != nil && last.Intent.Args.Metric != nil && last.Intent.Args.Metric.RED != nil {
			if args.Metric.RED.Service == "" {
				args.Metric.RED.Service = last.Intent.Args.Metric.RED.Service
			}
		}
	case IntentTopologyQuestion:
		if args.Topology != nil && last.Intent.Args.Topology != nil {
			if args.Topology.Service == "" {
				args.Topology.Service = last.Intent.Args.Topology.Service
			}
		}
	}
	return args
}
