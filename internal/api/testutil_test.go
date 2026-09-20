package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/auth"
	"traceiq/internal/config"
	"traceiq/internal/memory"
	"traceiq/internal/model"
	"traceiq/internal/nl"
	"traceiq/internal/rca"
	"traceiq/internal/rca/rules"
	"traceiq/internal/remediate"
	"traceiq/internal/store"
	"traceiq/internal/store/sqlite"
	"traceiq/internal/tenant"
	"traceiq/internal/topology"
)

// --- model.Clock ------------------------------------------------------
//
// model.NewRealClock() is "not implemented" (a sibling wave's TODO — see
// internal/model/clock.go's doc comment). Every real component this test
// wires (topology.NewLiveGraph, memory.NewInMemoryStore, rca.NewEngine)
// takes a model.Clock, so a minimal, real-wall-clock-backed implementation
// is supplied here, scoped to this package's tests only — the same pattern
// internal/rca/engine_test.go's fakeClock already establishes, just backed
// by real time instead of a settable one (nothing here needs to fast-
// forward simulated time).
type wallClock struct{}

func (wallClock) Now() time.Time                  { return time.Now() }
func (wallClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (wallClock) NewTicker(time.Duration) model.Ticker {
	panic("wallClock: NewTicker unused by api tests")
}
func (wallClock) NewTimer(time.Duration) model.Timer {
	panic("wallClock: NewTimer unused by api tests")
}
func (wallClock) Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ model.Clock = wallClock{}

// --- auth.Authenticator / auth.Authorizer seam -------------------------
//
// internal/auth ships interfaces only in this wave (no concrete
// implementation exists anywhere in the tree yet). Per the task brief this
// package follows the same injectable-seam pattern internal/nl already
// established for its own narrowed Authorizer: a fake supplies both halves
// here, and production wiring (cmd/traceiq, out of this task's scope)
// supplies its own.
type fakeAuthenticator struct{}

// Authenticate resolves a Subject from test-only "X-Test-Role"/
// "X-Test-Tenant" headers. No header at all yields a roleless Subject,
// which fakeAuthorizer below then denies every capability for — fail
// closed by construction, not by special-casing "missing header".
func (fakeAuthenticator) Authenticate(ctx context.Context, r *http.Request) (auth.Subject, error) {
	tenantID := r.Header.Get("X-Test-Tenant")
	if tenantID == "" {
		tenantID = "t1"
	}
	role := r.Header.Get("X-Test-Role")
	var roles []auth.Role
	if role != "" {
		roles = []auth.Role{auth.Role(role)}
	}
	return auth.Subject{
		ID:     "test:" + role,
		Tenant: model.TenantID(tenantID),
		Roles:  roles,
		Kind:   auth.SubjToken,
	}, nil
}

func (fakeAuthenticator) AuthenticateChat(ctx context.Context, p auth.ChatRequest) (auth.Subject, error) {
	return auth.Subject{}, nil
}

func (fakeAuthenticator) Close() error { return nil }

var _ auth.Authenticator = fakeAuthenticator{}

// fakeAuthorizer implements DR-25 §25.2's "capability matrix, not a role
// hierarchy" as literally as a stub can: each role maps to its OWN closed
// capability set below (admin is given every capability explicitly, not
// via a hierarchy short-circuit), and a Subject holding none of the
// matching roles is denied — fail closed.
type fakeAuthorizer struct{}

var roleCapabilities = map[auth.Role]map[auth.Capability]bool{
	auth.RoleViewer: {
		CapTelemetryRead: true,
	},
	auth.RoleOperator: {
		CapTelemetryRead:        true,
		CapIncidentInvestigate:  true,
		CapRemediatePropose:     true,
		CapInvestigationReplay:  true,
		CapInvestigationCorrect: true,
	},
	auth.RoleApprover: {
		CapTelemetryRead:        true,
		CapRemediateApprove:     true,
		CapInvestigationCorrect: true,
	},
	auth.RoleAdmin: {
		CapTelemetryRead:        true,
		CapIncidentInvestigate:  true,
		CapRemediatePropose:     true,
		CapRemediateApprove:     true,
		CapAdmin:                true,
		CapAuditRead:            true,
		CapInvestigationReplay:  true,
		CapInvestigationCorrect: true,
	},
}

func (fakeAuthorizer) Can(ctx context.Context, s auth.Subject, c auth.Capability, res auth.ResourceRef) error {
	for _, role := range s.Roles {
		if roleCapabilities[role][c] {
			return nil
		}
	}
	return errDenied{subject: s.ID, cap: c}
}

func (fakeAuthorizer) SeparationOfDuty(proposer, approver string) error {
	if proposer == approver {
		return errDenied{subject: proposer, cap: "separation_of_duty"}
	}
	return nil
}

func (fakeAuthorizer) Roles(s auth.Subject) []auth.Role { return s.Roles }

var _ auth.Authorizer = fakeAuthorizer{}

type errDenied struct {
	subject string
	cap     auth.Capability
}

func (e errDenied) Error() string {
	return "subject " + e.subject + " lacks capability " + string(e.cap)
}

// --- traceStoreAdapter: rca.TraceStore over the real store.HotIndex ------
//
// rca.ToolRegistry needs an rca.TraceStore (a package-declared, narrow
// seam per DR-2), satisfied here by projecting store.HotIndex.SearchSpans
// into model.ToolResult — the same conversion cmd/traceiq's production
// wiring will need, reconstructed minimally for this test's own use.
type traceStoreAdapter struct{ hot store.HotIndex }

func (a traceStoreAdapter) TraceQuery(ctx context.Context, tid model.TenantID, args rca.TraceQueryArgs) (model.ToolResult, error) {
	page, err := a.hot.SearchSpans(ctx, tid, store.SpanQuery{
		Service:   args.Service,
		Operation: args.Operation,
		Limit:     args.Limit,
	})
	if err != nil {
		return model.ToolResult{}, err
	}
	rows, err := json.Marshal(page.Spans)
	if err != nil {
		return model.ToolResult{}, err
	}
	return model.ToolResult{Rows: rows, Tool: rca.ToolTraceQuery, ObservedAt: time.Now()}, nil
}

var _ rca.TraceStore = traceStoreAdapter{}

// --- remediate.PolicyStore fake -------------------------------------------

type fakePolicyStore struct{ policy tenant.Policy }

func (f fakePolicyStore) Get(ctx context.Context, tid model.TenantID) (tenant.Policy, error) {
	return f.policy, nil
}

func testPolicy(tid model.TenantID) tenant.Policy {
	return tenant.Policy{
		TenantID:                tid,
		RemediationAllowlist:    []model.ActionType{model.ActionRestartPod, model.ActionScaleReplicas},
		NamespaceAllowlist:      []string{"default"},
		TargetAllowlist:         []string{"default/Pod/*", "default/Deployment/*"},
		ActionBudgetPerIncident: 5,
		MaxReplicas:             50,
	}
}

// fakeResolver/fakeSnapshotter/fakeSignalSource/fakeVerifier are minimal,
// always-succeed implementations of remediate's own consumer-declared
// interfaces (TargetResolver/Snapshotter/SignalSource/Verifier). There is
// no live Kubernetes cluster in this test process, so these stand in for
// "the target exists and is healthy," letting Guard's own state machine
// (propose -> approve -> budget/idempotency/separation-of-duty checks) run
// for real rather than being mocked away.
type fakeResolver struct{}

func (fakeResolver) Resolve(ctx context.Context, tid model.TenantID, target model.ActionTarget) (model.ActionTarget, error) {
	target.ResolvedUID = "uid-" + target.Name
	target.ResolvedVersion = "1"
	return target, nil
}

type fakeSnapshotter struct{}

func (fakeSnapshotter) Snapshot(ctx context.Context, tid model.TenantID, target model.ActionTarget) (model.Snapshot, error) {
	return model.Snapshot{TakenAt: time.Now()}, nil
}

type fakeSignalSource struct{}

func (fakeSignalSource) Signal(ctx context.Context, tid model.TenantID, a model.Action) (remediate.RecoverySignal, error) {
	return remediate.RecoverySignal{}, nil
}

type fakeVerifier struct{}

func (fakeVerifier) Verify(ctx context.Context, tid model.TenantID, a model.Action, s remediate.RecoverySignal) (model.VerifyResult, error) {
	return model.VerifyResult{}, nil
}

// --- test harness ---------------------------------------------------------

// testEnv wires every Deps field against REAL implementations (sqlite-
// backed store, in-memory topology/anomaly/memory/rca/remediate), never a
// mocked interface — per the task brief's "MCP tool calls ... work
// end-to-end against real (not mocked) store/topology data."
type testEnv struct {
	srv    *HTTPServer
	deps   Deps
	tenant model.TenantID
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	clock := wallClock{}
	tid := model.TenantID("t1")

	// store.HotIndex: real sqlite-backed store (modernc.org/sqlite, no CGO).
	dbPath := filepath.Join(t.TempDir(), "traceiq-test.db")
	hot, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { hot.Close() })

	// topology.Graph: real in-memory LiveGraph.
	graph := topology.NewLiveGraph(config.TopologyConfig{}, nil, nil, clock)

	// anomaly.Grouper: real in-memory grouper. graph (*topology.LiveGraph)
	// does not itself satisfy anomaly.TopologyReader (different Neighbors
	// signature, no Has method) — see topology_adapter.go for why and how
	// this is bridged.
	//
	// anomaly.DefaultConfig(), not the zero-value anomaly.Config{}: a zero
	// Grouping.MaxOpenIncidents makes enforceMaxOpenIncidents treat every
	// incident as over-budget (open(1) > max(0)) and immediately expire it
	// on creation, so ActiveIncidents always came back empty regardless of
	// the topology fix above (caught by TestIncidents_RealGrouper once the
	// build-breaking interface mismatch stopped masking it — see
	// docs/reports/w15-api-web.md).
	grouper := anomaly.NewGrouper(anomaly.DefaultConfig(), newAnomalyTopologyReader(graph))

	// memory.Store: real in-memory store.
	mem := memory.NewInMemoryStore(clock, memory.NewLexicalScorer())

	// rca.Engine: real engine, rules reasoner only (LLM wiring is a
	// sibling wave's concurrent task — out of this task's scope per the
	// brief).
	registry := rca.NewRegistry(
		traceStoreAdapter{hot: hot},
		nil, // LogStore: no log correlator wired this wave
		nil, // MetricStore
		nil, // TopologyStore
		nil, // MemoryStore
	)
	engine := rca.NewEngine(rca.NewMemJournal(), registry, rules.New(), rca.NewMemObjectStore(), nil, clock)

	// remediate.Guard: real guard; fake (always-succeed) cluster-facing
	// seams since there is no live Kubernetes cluster in this test process.
	guard := remediate.NewGuard(remediate.Deps{
		Policy:   fakePolicyStore{policy: testPolicy(tid)},
		Resolver: fakeResolver{},
		Snapshot: fakeSnapshotter{},
		Verifier: fakeVerifier{},
		Signals:  fakeSignalSource{},
		Authz:    fakeAuthorizer{},
		Now:      clock.Now,
	})

	// nl.Interpreter/Answerer: real rules interpreter + rules answerer.
	interp := nl.NewRulesInterpreter(func(ctx context.Context, tid model.TenantID) ([]string, error) {
		return []string{"checkout", "payments"}, nil
	}, clock)
	answerer := &nl.RulesAnswerer{
		Registry: registry,
		Engine:   engine,
		Grouper:  grouper,
		AuthZ:    fakeAuthorizer{},
		Clock:    clock,
	}

	deps := Deps{
		Auth:           fakeAuthenticator{},
		Authorizer:     fakeAuthorizer{},
		Topology:       graph,
		Interpreter:    interp,
		Answerer:       answerer,
		Investigations: engine,
		Memory:         mem,
		Anomaly:        grouper,
		Store:          hot,
		Remediate:      guard,
	}

	srv := NewHTTPServer("127.0.0.1:0", deps)
	return &testEnv{srv: srv, deps: deps, tenant: tid}
}

// do issues req through the server's mux directly (httptest.NewRecorder),
// so tests never depend on a bound TCP listener.
func (e *testEnv) request(method, path, role string, body []byte) *http.Request {
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	req.Header.Set("X-Test-Tenant", string(e.tenant))
	req.Header.Set("Content-Type", "application/json")
	return req
}
