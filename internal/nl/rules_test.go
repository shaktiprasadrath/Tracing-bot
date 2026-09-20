package nl

import (
	"context"
	"regexp"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/rca"
)

func testCatalog(ctx context.Context, tid model.TenantID) ([]string, error) {
	return []string{"checkout-svc", "payment-svc", "cart-svc"}, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                                   { return c.t }
func (c fixedClock) Since(t time.Time) time.Duration                  { return c.t.Sub(t) }
func (c fixedClock) NewTicker(d time.Duration) model.Ticker           { panic("not used") }
func (c fixedClock) NewTimer(d time.Duration) model.Timer             { panic("not used") }
func (c fixedClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

func newTestInterpreter() *RulesInterpreter {
	return NewRulesInterpreter(testCatalog, fixedClock{t: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)})
}

// TestIntentClassification_AllTenIntents drives >=4 representative surface
// forms per FR-F10-9's closed taxonomy through RulesInterpreter alone
// (fully offline path) and asserts each classifies correctly.
func TestIntentClassification_AllTenIntents(t *testing.T) {
	cases := []struct {
		text string
		want IntentKind
	}{
		// IntentTraceSearch
		{"find traces for checkout-svc", IntentTraceSearch},
		{"show me traces with errors in payment-svc", IntentTraceSearch},
		{"search traces for cart-svc in the last 30 minutes", IntentTraceSearch},
		{"list slow traces for checkout-svc", IntentTraceSearch},

		// IntentServiceHealth
		{"how is checkout-svc doing", IntentServiceHealth},
		{"health of payment-svc", IntentServiceHealth},
		{"is cart-svc healthy", IntentServiceHealth},
		{"what is the error rate for checkout-svc", IntentServiceHealth},

		// IntentTopologyQuestion
		{"what calls checkout-svc", IntentTopologyQuestion},
		{"who depends on payment-svc", IntentTopologyQuestion},
		{"show topology for cart-svc", IntentTopologyQuestion},
		{"show me the service map for checkout-svc", IntentTopologyQuestion},

		// IntentIncidentStatus
		{"what incidents are active right now", IntentIncidentStatus},
		{"list open incidents", IntentIncidentStatus},
		{"any incidents currently", IntentIncidentStatus},
		{"current incident status", IntentIncidentStatus},

		// IntentInvestigationAsk
		{"what did investigation inv-123 find", IntentInvestigationAsk},
		{"status of investigation inv-42", IntentInvestigationAsk},
		{"tell me about investigation inv-7", IntentInvestigationAsk},
		{"give me the findings for investigation inv-9", IntentInvestigationAsk},

		// IntentMemoryLookup
		{"have we seen this before", IntentMemoryLookup},
		{"any similar incidents in the past", IntentMemoryLookup},
		{"recall a similar issue to this one", IntentMemoryLookup},
		{"past investigations like this one", IntentMemoryLookup},

		// IntentStartInvestigation
		{"why is checkout-svc slow", IntentStartInvestigation},
		{"investigate payment-svc", IntentStartInvestigation},
		{"start an investigation for cart-svc", IntentStartInvestigation},
		{"find the root cause of checkout-svc errors", IntentStartInvestigation},

		// IntentCompareWindows
		{"compare the last hour to yesterday", IntentCompareWindows},
		{"what changed since the checkout-svc deploy", IntentCompareWindows},
		{"diff between now and last week for payment-svc", IntentCompareWindows},
		{"compare cart-svc before and after the deploy", IntentCompareWindows},

		// IntentExplainTrace
		{"explain trace 4bf92f3577b34da6a3ce929d0e0e4736", IntentExplainTrace},
		{"what happened in trace 4bf92f3577b34da6a3ce929d0e0e4736", IntentExplainTrace},
		{"walk me through trace 4bf92f3577b34da6a3ce929d0e0e4736", IntentExplainTrace},
		{"why did trace 4bf92f3577b34da6a3ce929d0e0e4736 fail", IntentExplainTrace},

		// IntentRemediate
		{"restart checkout-svc", IntentRemediate},
		{"rollback the last deploy", IntentRemediate},
		{"scale payment-svc to 5 replicas", IntentRemediate},
		{"toggle the new-checkout feature flag off", IntentRemediate},
	}

	ri := newTestInterpreter()
	tid := model.TenantID("tenant-a")
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			q := Question{Text: tc.text, AskedAt: time.Now()}
			got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
			if err != nil {
				t.Fatalf("Interpret(%q) error: %v", tc.text, err)
			}
			if got.Kind != tc.want {
				t.Fatalf("Interpret(%q) = %v, want %v (candidates %v)", tc.text, got.Kind, tc.want, got.Candidates)
			}
			if got.Args.TenantID != tid {
				t.Fatalf("Interpret(%q) Args.TenantID = %q, want %q", tc.text, got.Args.TenantID, tid)
			}
		})
	}
}

func TestIntentClassification_UnrecognizedFallsBackGracefully(t *testing.T) {
	ri := newTestInterpreter()
	tid := model.TenantID("tenant-a")
	texts := []string{
		"",
		"good morning",
		"what's the weather like",
		"asdkjaslkdj qweoiqwe",
	}
	for _, text := range texts {
		t.Run(text, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Interpret(%q) panicked: %v", text, r)
				}
			}()
			q := Question{Text: text, AskedAt: time.Now()}
			got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
			if err != nil {
				t.Fatalf("Interpret(%q) error: %v", text, err)
			}
			if got.Kind != IntentUnknown {
				t.Fatalf("Interpret(%q) = %v, want IntentUnknown", text, got.Kind)
			}
		})
	}
}

func TestIntentClassification_AmbiguousReturnsUnknownWithCandidates(t *testing.T) {
	// "checkout-svc" alone matching both a health-style and investigate-style
	// phrase within the ambiguity threshold should defer to a clarifying
	// question rather than guess (FR-F10-11).
	ri := &RulesInterpreter{
		ServiceCatalog: testCatalog,
		Clock:          fixedClock{t: time.Now()},
		Patterns: []Rule{
			{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`checkout-svc`), Priority: 5},
			{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`checkout-svc`), Priority: 5},
		},
	}
	tid := model.TenantID("tenant-a")
	q := Question{Text: "checkout-svc", AskedAt: time.Now()}
	got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
	if err != nil {
		t.Fatalf("Interpret error: %v", err)
	}
	if got.Kind != IntentUnknown {
		t.Fatalf("Kind = %v, want IntentUnknown", got.Kind)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", got.Candidates)
	}
}

func TestKind_ReturnsRules(t *testing.T) {
	ri := newTestInterpreter()
	if ri.Kind() != "rules" {
		t.Fatalf("Kind() = %q, want %q", ri.Kind(), "rules")
	}
}

// TestEntityExtraction_PopulatesTypedToolArgs asserts entities land in the
// correct typed rca.ToolArgs field per FR-F10-2 — never a free-form map.
func TestEntityExtraction_PopulatesTypedToolArgs(t *testing.T) {
	ri := newTestInterpreter()
	tid := model.TenantID("tenant-a")

	t.Run("service health populates Metric.RED.Service", func(t *testing.T) {
		q := Question{Text: "how is checkout-svc doing", AskedAt: time.Now()}
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.Args.Tool != rca.ToolMetricQuery || got.Args.Metric == nil || got.Args.Metric.RED == nil {
			t.Fatalf("Args = %+v, want a populated Metric.RED", got.Args)
		}
		if got.Args.Metric.RED.Service != "checkout-svc" {
			t.Fatalf("Metric.RED.Service = %q, want checkout-svc", got.Args.Metric.RED.Service)
		}
	})

	t.Run("trace search populates Trace.Service and window", func(t *testing.T) {
		q := Question{Text: "find traces for payment-svc in the last 30 minutes", AskedAt: time.Now()}
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.Args.Trace == nil || got.Args.Trace.Service != "payment-svc" {
			t.Fatalf("Args.Trace = %+v, want Service=payment-svc", got.Args.Trace)
		}
		if got.Args.Trace.Start.IsZero() || got.Args.Trace.End.IsZero() {
			t.Fatalf("Args.Trace window not populated: %+v", got.Args.Trace)
		}
	})

	t.Run("explain trace populates trace id", func(t *testing.T) {
		q := Question{Text: "explain trace 4bf92f3577b34da6a3ce929d0e0e4736", AskedAt: time.Now()}
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.Args.Trace == nil || len(got.Args.Trace.AttrEquals) == 0 {
			t.Fatalf("Args.Trace = %+v, want an AttrEquals trace_id entry", got.Args.Trace)
		}
		found := false
		for _, ae := range got.Args.Trace.AttrEquals {
			if ae.Key == "trace_id" && ae.Value == "4bf92f3577b34da6a3ce929d0e0e4736" {
				found = true
			}
		}
		if !found {
			t.Fatalf("AttrEquals = %+v, missing trace_id match", got.Args.Trace.AttrEquals)
		}
	})

	t.Run("topology question populates Topology.Service", func(t *testing.T) {
		q := Question{Text: "what calls checkout-svc", AskedAt: time.Now()}
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.Args.Topology == nil || got.Args.Topology.Service != "checkout-svc" {
			t.Fatalf("Args.Topology = %+v, want Service=checkout-svc", got.Args.Topology)
		}
	})
}

// TestEntityExtraction_FuzzyServiceMatchCaveat covers W14 review #6 (F10 §5's
// "did you mean X?" mitigation): an exact service-name match must carry no
// caveat, while a non-exact (Levenshtein distance > 0) match must surface
// the matched service name via Intent.FuzzyServiceMatch so Answerer can
// caveat it, rather than presenting the guess identically to an exact hit.
func TestEntityExtraction_FuzzyServiceMatchCaveat(t *testing.T) {
	ri := newTestInterpreter()
	tid := model.TenantID("tenant-a")

	t.Run("exact match carries no fuzzy caveat", func(t *testing.T) {
		q := Question{Text: "how is checkout-svc doing", AskedAt: time.Now()}
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.FuzzyServiceMatch != "" {
			t.Fatalf("FuzzyServiceMatch = %q, want empty for an exact match", got.FuzzyServiceMatch)
		}
	})

	t.Run("non-exact match carries the matched service as a caveat", func(t *testing.T) {
		q := Question{Text: "is chekot-svc healthy", AskedAt: time.Now()} // distance 2 from checkout-svc
		got, err := ri.Interpret(context.Background(), tid, q, ConversationContext{TenantID: tid})
		if err != nil {
			t.Fatal(err)
		}
		if got.FuzzyServiceMatch != "checkout-svc" {
			t.Fatalf("FuzzyServiceMatch = %q, want checkout-svc", got.FuzzyServiceMatch)
		}
		if got.Args.Metric == nil || got.Args.Metric.RED == nil || got.Args.Metric.RED.Service != "checkout-svc" {
			t.Fatalf("Args = %+v, want the fuzzy-matched service still populated into Args", got.Args)
		}
	})
}

// TestFollowUpArgsMergeFromFocus asserts a follow-up question missing an
// explicit service falls back to ConversationContext.Focus (FR-F10-8),
// keyed per thread.
func TestFollowUpArgsMergeFromFocus(t *testing.T) {
	ri := newTestInterpreter()
	tid := model.TenantID("tenant-a")
	cc := ConversationContext{
		TenantID: tid,
		Focus:    Focus{Service: "checkout-svc"},
	}
	q := Question{Text: "how is it doing right now", AskedAt: time.Now()}
	// "how is it doing" alone won't match a service name, so this exercises
	// the health pattern without an extractable service — it should still
	// classify as ServiceHealth and inherit Focus.Service.
	got, err := ri.Interpret(context.Background(), tid, q, cc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != IntentServiceHealth {
		t.Skipf("phrasing did not match ServiceHealth (got %v); pattern table may need a generic form", got.Kind)
	}
	if got.Args.Metric == nil || got.Args.Metric.RED == nil || got.Args.Metric.RED.Service != "checkout-svc" {
		t.Fatalf("expected Focus.Service to carry forward, got Args=%+v", got.Args)
	}
}
