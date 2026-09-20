package nl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/model"
	"traceiq/internal/rca"
)

var errDenied = errors.New("denied")

// fakeRegistry is a directly-programmable rca.ToolRegistry test double
// (mirrors internal/rca's own fakeRegistry pattern) that records the tenant
// each Dispatch call actually used, so tests can assert nl never lets a
// crafted/mismatched Args.TenantID override the server-resolved tenant.
type fakeRegistry struct {
	dispatchedTenants []model.TenantID
	rowsByTenant      map[model.TenantID][]byte
	err               error
}

func (f *fakeRegistry) Get(n model.ToolName) (rca.Tool, bool) { return nil, false }
func (f *fakeRegistry) Names() []model.ToolName               { return nil }
func (f *fakeRegistry) Dispatch(ctx context.Context, tid model.TenantID, a rca.ToolArgs) (model.ToolResult, error) {
	f.dispatchedTenants = append(f.dispatchedTenants, tid)
	if f.err != nil {
		return model.ToolResult{}, f.err
	}
	return model.ToolResult{
		Rows:       f.rowsByTenant[tid],
		Tool:       a.Tool,
		ObservedAt: time.Now(),
	}, nil
}

var _ rca.ToolRegistry = (*fakeRegistry)(nil)

func newTestAnswerer(reg *fakeRegistry) *RulesAnswerer {
	return &RulesAnswerer{Registry: reg}
}

func TestAnswer_ClarifyingQuestionForUnknownIntent(t *testing.T) {
	a := newTestAnswerer(&fakeRegistry{})
	in := Intent{Kind: IntentUnknown, Candidates: []IntentKind{IntentServiceHealth, IntentStartInvestigation}}
	got, err := a.Answer(context.Background(), model.TenantID("t1"), in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Clarifying {
		t.Fatalf("Answer.Clarifying = false, want true for IntentUnknown")
	}
	if got.Text == "" {
		t.Fatal("expected a non-empty clarifying question")
	}
}

// TestAnswer_CitationsAreReal ensures Answer.Evidence/Citations trace back
// to the ACTUAL bytes the fake registry returned for this tenant — not a
// fabricated string — satisfying FR-F10-5's "MUST NOT state ... not backed
// by an EvidenceRef".
func TestAnswer_CitationsAreReal(t *testing.T) {
	tid := model.TenantID("tenant-a")
	rows := []byte(`[{"service":"checkout-svc","p99_ms":842}]`)
	reg := &fakeRegistry{rowsByTenant: map[model.TenantID][]byte{tid: rows}}
	a := newTestAnswerer(reg)

	in := Intent{
		Kind: IntentServiceHealth,
		Args: rca.ToolArgs{
			TenantID: tid,
			Tool:     rca.ToolMetricQuery,
			Metric:   &rca.MetricQueryArgs{RED: &rca.REDQuery{Service: "checkout-svc"}},
		},
	}
	got, err := a.Answer(context.Background(), tid, in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Evidence) == 0 {
		t.Fatal("expected >= 1 Evidence entry (FR-F10-5)")
	}
	found := false
	for _, ev := range got.Evidence {
		if string(ev.PayloadJSON) == string(rows) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Evidence PayloadJSON does not match the dispatched ToolResult.Rows; citations are not real. Evidence=%+v", got.Evidence)
	}
	if len(got.Citations) == 0 {
		t.Fatal("expected >= 1 Citation referencing the Evidence")
	}
}

// TestAnswer_ExactServiceMatchNoCaveat and TestAnswer_FuzzyServiceMatchCaveat
// cover W14 review #6 / F10 §5's documented mitigation: a non-exact
// service-name match must be surfaced inline with a "did you mean X?"
// caveat rather than presented identically to an exact hit.
func TestAnswer_ExactServiceMatchNoCaveat(t *testing.T) {
	tid := model.TenantID("tenant-a")
	reg := &fakeRegistry{rowsByTenant: map[model.TenantID][]byte{tid: []byte(`[]`)}}
	a := newTestAnswerer(reg)

	in := Intent{
		Kind: IntentServiceHealth,
		Args: rca.ToolArgs{
			TenantID: tid,
			Tool:     rca.ToolMetricQuery,
			Metric:   &rca.MetricQueryArgs{RED: &rca.REDQuery{Service: "checkout-svc"}},
		},
		// FuzzyServiceMatch left empty: extractToolArgs only sets it for a
		// non-exact match, so an exact "checkout-svc" hit never populates it.
	}
	got, err := a.Answer(context.Background(), tid, in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "did you mean") {
		t.Fatalf("Answer.Text = %q, must not carry a fuzzy-match caveat for an exact match", got.Text)
	}
}

func TestAnswer_FuzzyServiceMatchCaveat(t *testing.T) {
	tid := model.TenantID("tenant-a")
	reg := &fakeRegistry{rowsByTenant: map[model.TenantID][]byte{tid: []byte(`[]`)}}
	a := newTestAnswerer(reg)

	in := Intent{
		Kind: IntentServiceHealth,
		Args: rca.ToolArgs{
			TenantID: tid,
			Tool:     rca.ToolMetricQuery,
			Metric:   &rca.MetricQueryArgs{RED: &rca.REDQuery{Service: "checkout-svc"}},
		},
		FuzzyServiceMatch: "checkout-svc", // as extractToolArgs would set for e.g. "chekot-svc"
	}
	got, err := a.Answer(context.Background(), tid, in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "did you mean") || !strings.Contains(got.Text, "checkout-svc") {
		t.Fatalf("Answer.Text = %q, want a 'did you mean checkout-svc?' caveat", got.Text)
	}
}

// TestAnswer_FuzzyCaveatOmittedOnClarifyingAnswer ensures the caveat is never
// appended to a Clarifying answer (permission refusals, disambiguation
// questions) — that would leak a guessed service name past an authorization
// failure the caller never actually got to act on.
func TestAnswer_FuzzyCaveatOmittedOnClarifyingAnswer(t *testing.T) {
	a := &RulesAnswerer{Registry: &fakeRegistry{}} // AuthZ nil -> fails closed, Clarifying: true
	in := Intent{
		Kind:              IntentStartInvestigation,
		Args:              rca.ToolArgs{Trace: &rca.TraceQueryArgs{Service: "checkout-svc"}},
		FuzzyServiceMatch: "checkout-svc",
	}
	got, err := a.Answer(context.Background(), model.TenantID("t1"), in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Clarifying {
		t.Fatalf("expected a Clarifying refusal with nil AuthZ, got %+v", got)
	}
	if strings.Contains(got.Text, "did you mean") {
		t.Fatalf("Answer.Text = %q, must not leak a fuzzy-match caveat on a Clarifying refusal", got.Text)
	}
}

// TestAnswer_TenantIsolation is the key security property: even if a
// caller's Intent.Args somehow carried a different tenant (e.g. built from
// stale/crafted state), Answer must dispatch using the server-resolved tid
// parameter — never Args.TenantID — so tenant-a's answer can never surface
// tenant-b's rows.
func TestAnswer_TenantIsolation(t *testing.T) {
	victim := model.TenantID("tenant-victim")
	attacker := model.TenantID("tenant-attacker")
	reg := &fakeRegistry{rowsByTenant: map[model.TenantID][]byte{
		victim:   []byte(`{"secret":"victim-only-data"}`),
		attacker: []byte(`{"public":"attacker-data"}`),
	}}
	a := newTestAnswerer(reg)

	// Args.TenantID is deliberately wrong/crafted; the resolved tid passed
	// to Answer (as it would be from auth.Subject.Tenant, DR-5) is "victim".
	in := Intent{
		Kind: IntentServiceHealth,
		Args: rca.ToolArgs{
			TenantID: attacker,
			Tool:     rca.ToolMetricQuery,
			Metric:   &rca.MetricQueryArgs{RED: &rca.REDQuery{Service: "checkout-svc"}},
		},
	}
	got, err := a.Answer(context.Background(), victim, in, ConversationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.dispatchedTenants) != 1 || reg.dispatchedTenants[0] != victim {
		t.Fatalf("Dispatch was called with tenant(s) %v, want exactly [%q] (server-resolved tid, never Args.TenantID)", reg.dispatchedTenants, victim)
	}
	for _, ev := range got.Evidence {
		if string(ev.PayloadJSON) == `{"public":"attacker-data"}` {
			t.Fatal("cross-tenant leak: victim's Answer contains attacker-tenant rows")
		}
	}
}

func TestAnswer_RemediateRequiresCapability(t *testing.T) {
	tid := model.TenantID("t1")
	reg := &fakeRegistry{}
	proposer := &fakeProposer{}
	a := &RulesAnswerer{
		Registry:  reg,
		Remediate: proposer,
		AuthZ:     denyAuthorizer{},
	}
	in := Intent{Kind: IntentRemediate, Args: rca.ToolArgs{TenantID: tid}}
	subject := auth.Subject{ID: "tok_1", Tenant: tid, Roles: []auth.Role{auth.RoleViewer}}
	got, err := a.AnswerForSubject(context.Background(), tid, in, ConversationContext{}, subject)
	if err != nil {
		t.Fatal(err)
	}
	if proposer.called {
		t.Fatal("Guard.Propose must not be called when the subject lacks remediation_action:propose (FR-F10-6)")
	}
	if !got.Clarifying && got.Text == "" {
		t.Fatal("expected a refusal/clarifying answer, got empty")
	}
}

func TestAnswer_RemediateNeverExecutesOnlyProposes(t *testing.T) {
	tid := model.TenantID("t1")
	reg := &fakeRegistry{}
	proposer := &fakeProposer{}
	a := &RulesAnswerer{
		Registry:  reg,
		Remediate: proposer,
		AuthZ:     allowAuthorizer{},
	}
	in := Intent{Kind: IntentRemediate, Args: rca.ToolArgs{TenantID: tid}, Raw: "restart checkout-svc"}
	subject := auth.Subject{ID: "tok_1", Tenant: tid, Roles: []auth.Role{auth.RoleOperator}}
	_, err := a.AnswerForSubject(context.Background(), tid, in, ConversationContext{}, subject)
	if err != nil {
		t.Fatal(err)
	}
	if !proposer.called {
		t.Fatal("expected Remediate.Propose to be called once capability check passes")
	}
	if proposer.executed {
		t.Fatal("nl must never execute a remediation directly, only propose (FR-F10-6)")
	}
}

// TestAnswer_RemediateNilAuthZFailsClosed is a regression test for a
// FAIL-OPEN bug found in review: with AuthZ == nil (an unwired/omitted
// dependency), the code used to skip the capability check entirely rather
// than refusing. remediate.Guard.Propose does NOT itself re-check
// "remediation_action:propose" (its Authz field only gates SeparationOfDuty
// at Approve time) — nl.RulesAnswerer's own check is the ONLY enforcement of
// FR-F10-6/AC-F10-6, so a nil AuthZ must refuse, never silently allow.
func TestAnswer_RemediateNilAuthZFailsClosed(t *testing.T) {
	tid := model.TenantID("t1")
	proposer := &fakeProposer{}
	a := &RulesAnswerer{
		Registry:  &fakeRegistry{},
		Remediate: proposer,
		AuthZ:     nil, // deliberately unwired
	}
	in := Intent{Kind: IntentRemediate, Args: rca.ToolArgs{TenantID: tid}, Raw: "restart checkout-svc"}
	subject := auth.Subject{ID: "tok_1", Tenant: tid, Roles: []auth.Role{auth.RoleOperator}}
	got, err := a.AnswerForSubject(context.Background(), tid, in, ConversationContext{}, subject)
	if err != nil {
		t.Fatal(err)
	}
	if proposer.called {
		t.Fatal("Guard.Propose must not be called when AuthZ is nil (unconfigured gate must fail closed)")
	}
	if !got.Clarifying {
		t.Fatalf("expected a refusal (Clarifying=true) when AuthZ is nil, got %+v", got)
	}
}

// TestAnswer_StartInvestigationNilAuthZFailsClosed mirrors the remediate
// case for IntentStartInvestigation: rca.Engine.Investigate has no
// independent capability check of its own, so nl's AuthZ.Can is the sole
// gate for "incident:investigate" (FR-F10-7) and must refuse, not skip, when
// unconfigured.
func TestAnswer_StartInvestigationNilAuthZFailsClosed(t *testing.T) {
	tid := model.TenantID("t1")
	eng := &fakeEngine{}
	a := &RulesAnswerer{
		Registry: &fakeRegistry{},
		Engine:   eng,
		AuthZ:    nil, // deliberately unwired
	}
	in := Intent{Kind: IntentStartInvestigation, Args: rca.ToolArgs{TenantID: tid}, Raw: "investigate checkout-svc"}
	subject := auth.Subject{ID: "tok_1", Tenant: tid, Roles: []auth.Role{auth.RoleOperator}}
	got, err := a.AnswerForSubject(context.Background(), tid, in, ConversationContext{}, subject)
	if err != nil {
		t.Fatal(err)
	}
	if eng.called {
		t.Fatal("Engine.Investigate must not be called when AuthZ is nil (unconfigured gate must fail closed)")
	}
	if !got.Clarifying {
		t.Fatalf("expected a refusal (Clarifying=true) when AuthZ is nil, got %+v", got)
	}
}

type fakeEngine struct {
	called bool
}

func (f *fakeEngine) Investigate(ctx context.Context, tid model.TenantID, inc model.Incident) (model.Investigation, error) {
	f.called = true
	return model.Investigation{ID: "inv-1", Tenant: tid}, nil
}

func (f *fakeEngine) Get(ctx context.Context, tid model.TenantID, id string) (model.Investigation, error) {
	return model.Investigation{}, nil
}

func (f *fakeEngine) Replay(ctx context.Context, tid model.TenantID, investigationID string, mode rca.ReplayMode) (model.Investigation, error) {
	return model.Investigation{}, nil
}

func (f *fakeEngine) Correct(ctx context.Context, tid model.TenantID, investigationID, stepID string, c model.Correction) (model.Investigation, error) {
	return model.Investigation{}, nil
}

func (f *fakeEngine) Abort(ctx context.Context, tid model.TenantID, investigationID, reason string) error {
	return nil
}

func (f *fakeEngine) Stats() rca.Stats { return rca.Stats{} }

var _ rca.Engine = (*fakeEngine)(nil)

type fakeProposer struct {
	called, executed bool
}

func (f *fakeProposer) Propose(ctx context.Context, tid model.TenantID, p RemediateProposal, subject auth.Subject) (string, error) {
	f.called = true
	return "action-1", nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) Can(ctx context.Context, s auth.Subject, c auth.Capability, res auth.ResourceRef) error {
	return errDenied
}

type allowAuthorizer struct{}

func (allowAuthorizer) Can(ctx context.Context, s auth.Subject, c auth.Capability, res auth.ResourceRef) error {
	return nil
}
