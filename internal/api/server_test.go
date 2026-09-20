package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

// insertTestTrace writes one real trace+span through store.HotIndex.WriteBatch
// (the same path the ingest pipeline uses) so REST/MCP read handlers are
// exercised against real, queryable data rather than a mock.
func insertTestTrace(t *testing.T, hot store.HotIndex, tid model.TenantID, service string) model.TraceID {
	t.Helper()
	var id model.TraceID
	copy(id[:], []byte("0123456789abcdef"))
	now := time.Now()
	_, err := hot.WriteBatch(context.Background(), tid, store.HotBatch{
		Traces: []store.TraceIndex{{
			Tenant:        tid,
			TraceID:       id,
			RootService:   service,
			StartUnixNano: now.UnixNano(),
			DurationNanos: 12_000_000,
			KeepReason:    model.KeepError,
		}},
		Spans: []store.SpanIndex{{
			Tenant:        tid,
			TraceID:       id,
			Service:       service,
			Operation:     "GET /checkout",
			StartUnixNano: now.UnixNano(),
			DurationNanos: 12_000_000,
		}},
	})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	return id
}

// --- FR-F12: each implemented REST endpoint returns correct status+shape. --

func TestHealthzReadyz(t *testing.T) {
	env := newTestEnv(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		env.srv.Routes().ServeHTTP(rec, env.request("GET", path, "", nil))
		if rec.Code != 200 {
			t.Fatalf("%s: want 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestOverview_ViewerAllowed(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/overview", "viewer", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body overviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
}

func TestOverview_UnauthorizedCallerRejected(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	// No role header at all: fakeAuthenticator still resolves a (roleless)
	// Subject, so this exercises the AUTHORIZATION gate, not authentication.
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/overview", "", nil))
	if rec.Code != 403 {
		t.Fatalf("want 403 for a roleless caller, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTracesSearch_RealStore(t *testing.T) {
	env := newTestEnv(t)
	insertTestTrace(t, env.deps.Store, env.tenant, "checkout")

	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/traces?service=checkout", "viewer", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var page store.TracePage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Traces) != 1 {
		t.Fatalf("want 1 trace from the real store, got %d (body=%s)", len(page.Traces), rec.Body.String())
	}
	if page.Traces[0].RootService != "checkout" {
		t.Fatalf("want RootService=checkout, got %q", page.Traces[0].RootService)
	}
}

func TestTraceGet_RealStore(t *testing.T) {
	env := newTestEnv(t)
	id := insertTestTrace(t, env.deps.Store, env.tenant, "checkout")

	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/traces/"+traceIDHex(id), "viewer", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTraceGet_NotFound(t *testing.T) {
	env := newTestEnv(t)
	var missing model.TraceID
	copy(missing[:], []byte("ffffffffffffffff"))
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/traces/"+traceIDHex(missing), "viewer", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTraceGet_InvalidID(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/traces/not-hex", "viewer", nil))
	if rec.Code != 400 {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTopology_ViewerAllowed(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/topology", "viewer", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIncidents_RealGrouper(t *testing.T) {
	env := newTestEnv(t)
	// Seed one real incident through anomaly.Grouper.Add (real component,
	// not a mock).
	_, _, err := env.deps.Anomaly.Add(context.Background(), env.tenant, model.AnomalyEvent{
		ID:          "ev-1",
		Tenant:      env.tenant,
		Kind:        model.AnomalyErrorBurst,
		Service:     "checkout",
		WindowStart: time.Now().Add(-time.Minute),
		WindowEnd:   time.Now(),
		Score:       0.9,
		Severity:    model.SeverityCritical,
	})
	if err != nil {
		t.Fatalf("Grouper.Add: %v", err)
	}

	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/v1/incidents", "viewer", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Incidents []model.Incident `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Incidents) != 1 {
		t.Fatalf("want 1 incident, got %d (body=%s)", len(out.Incidents), rec.Body.String())
	}
}

// --- Remediation: propose (operator) / approve (approver), never auto-exec. -

func TestActionsPropose_OperatorAllowed_ViewerDenied(t *testing.T) {
	env := newTestEnv(t)
	body, _ := json.Marshal(proposeActionRequest{
		Type: model.ActionRestartPod,
		Target: model.ActionTarget{
			Namespace: "default",
			Kind:      model.KindPod,
			Name:      "checkout-1",
		},
		Rationale: "restart to clear stuck connections",
		Restart:   &model.RestartPodSpec{GracePeriodSeconds: 30},
	})

	// Viewer: denied.
	rec := httptest.NewRecorder()
	req := env.request("POST", "/v1/actions", "viewer", body)
	req.Header.Set("Idempotency-Key", "idem-viewer-1")
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("viewer propose: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Operator: allowed, real Guard state machine runs.
	rec = httptest.NewRecorder()
	req = env.request("POST", "/v1/actions", "operator", body)
	req.Header.Set("Idempotency-Key", "idem-operator-1")
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("operator propose: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var action model.Action
	if err := json.Unmarshal(rec.Body.Bytes(), &action); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if action.ID == "" {
		t.Fatalf("expected a real Action.ID from Guard.Propose, got empty")
	}
	if action.State == model.ActionExecuting || action.State == model.ActionSucceeded {
		t.Fatalf("propose must never auto-execute; got state %v", action.State)
	}
}

func TestActionsPropose_MissingIdempotencyKey(t *testing.T) {
	env := newTestEnv(t)
	body, _ := json.Marshal(proposeActionRequest{Type: model.ActionRestartPod})
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("POST", "/v1/actions", "operator", body))
	if rec.Code != 400 {
		t.Fatalf("want 400 idempotency_key_required, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestActionsApprove_RBAC(t *testing.T) {
	env := newTestEnv(t)
	proposeBody, _ := json.Marshal(proposeActionRequest{
		Type: model.ActionRestartPod,
		Target: model.ActionTarget{
			Namespace: "default",
			Kind:      model.KindPod,
			Name:      "checkout-2",
		},
		Rationale: "restart",
		Restart:   &model.RestartPodSpec{GracePeriodSeconds: 10},
	})
	rec := httptest.NewRecorder()
	req := env.request("POST", "/v1/actions", "operator", proposeBody)
	req.Header.Set("Idempotency-Key", "idem-approve-flow")
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("propose: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var action model.Action
	if err := json.Unmarshal(rec.Body.Bytes(), &action); err != nil {
		t.Fatalf("decode: %v", err)
	}

	approveBody, _ := json.Marshal(approveActionRequest{Comment: "looks safe"})

	// Operator cannot approve (wrong capability).
	rec = httptest.NewRecorder()
	req = env.request("POST", "/v1/actions/"+action.ID+"/approve", "operator", approveBody)
	req.Header.Set("Idempotency-Key", "idem-approve-1")
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("operator approve: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Approver can approve.
	rec = httptest.NewRecorder()
	req = env.request("POST", "/v1/actions/"+action.ID+"/approve", "approver", approveBody)
	req.Header.Set("Idempotency-Key", "idem-approve-2")
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("approver approve: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Chat: NL query against the real rules Interpreter/Answerer. ----------

func TestNLQuery_Real(t *testing.T) {
	env := newTestEnv(t)
	body, _ := json.Marshal(nlQueryRequest{Text: "are there any active incidents"})
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("POST", "/v1/nl/query", "viewer", body))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- FR-F12-1: the embedded web/ assets are actually served. --------------

func TestStaticRoot_ServesRealHTML(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/", "", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct == "" || bytes.Contains(rec.Body.Bytes(), []byte("404")) {
		t.Fatalf("expected real HTML, got content-type=%q body=%s", ct, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("TraceIQ")) {
		t.Fatalf("expected the embedded index.html to mention TraceIQ, got: %s", rec.Body.String())
	}
}

func TestStaticSPARoute_FallsBackToIndex(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/traces", "", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200 (SPA fallback), got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("TraceIQ")) {
		t.Fatalf("expected index.html fallback content, got: %s", rec.Body.String())
	}
}

func TestStaticAsset_JS(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	env.srv.Routes().ServeHTTP(rec, env.request("GET", "/js/app.js", "", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}
