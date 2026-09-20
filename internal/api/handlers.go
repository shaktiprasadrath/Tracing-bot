package api

import (
	"net/http"
	"strconv"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/nl"
	"traceiq/internal/remediate"
	"traceiq/internal/store"
)

// --- helpers -------------------------------------------------------------

// windowFromQuery reads ?from=&to= (RFC3339) with a 1h default lookback, the
// same default every screen in F12 §4.4's UI table implies for an omitted
// range.
func windowFromQuery(r *http.Request, now time.Time) model.Window {
	w := model.Window{Start: now.Add(-1 * time.Hour), End: now}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			w.Start = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			w.End = t
		}
	}
	return w
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// --- GET /v1/overview (FR-F12-2, screen: Overview) -----------------------
//
// RED tiles + active-incident list, the two data sources the Overview
// screen's F12 §4.4 row names (`/v1/overview`, `/v1/incidents`). This
// handler folds both into one payload so the SPA's Overview page issues a
// single fetch.
type overviewResponse struct {
	Incidents []model.Incident `json:"incidents"`
	RED       []redTile        `json:"red_tiles"`
	GroupedAt time.Time        `json:"grouped_at"`
}

type redTile struct {
	Service   string            `json:"service"`
	Operation string            `json:"operation"`
	Samples   []model.REDSample `json:"samples"`
}

func (s *HTTPServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	subject, _ := subjectFrom(r.Context())
	ctx := r.Context()

	var incidents []model.Incident
	if s.Deps.Anomaly != nil {
		var err error
		incidents, err = s.Deps.Anomaly.ActiveIncidents(ctx, subject.Tenant)
		if err != nil {
			writeError(w, http.StatusBadGateway, "anomaly_unavailable", err.Error())
			return
		}
	}

	var tiles []redTile
	if s.Deps.Store != nil {
		win := windowFromQuery(r, time.Now())
		services := r.URL.Query()["service"]
		for _, svc := range services {
			series, err := s.Deps.Store.QueryRED(ctx, subject.Tenant, svc, "", win)
			if err != nil {
				continue
			}
			tiles = append(tiles, redTile{Service: svc, Samples: series.Samples})
		}
	}

	writeJSON(w, http.StatusOK, overviewResponse{Incidents: incidents, RED: tiles, GroupedAt: time.Now()})
}

// --- GET /v1/traces (FR-F12-3, screen: Traces search) --------------------

func (s *HTTPServer) handleTracesSearch(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "trace store is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	q := store.TraceQuery{
		Window:     windowFromQuery(r, time.Now()),
		Service:    r.URL.Query().Get("service"),
		ErrorsOnly: r.URL.Query().Get("errors_only") == "true",
		Limit:      queryInt(r, "limit", 50),
		PageToken:  r.URL.Query().Get("page_token"),
	}
	if v := r.URL.Query().Get("min_duration_ms"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			q.MinDuration = time.Duration(ms) * time.Millisecond
		}
	}
	page, err := s.Deps.Store.SearchTraces(r.Context(), subject.Tenant, q)
	if err != nil {
		writeError(w, http.StatusBadGateway, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// --- GET /v1/traces/{id} (FR-F12-3, screen: Traces waterfall) ------------

func (s *HTTPServer) handleTraceGet(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "trace store is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	id, err := parseTraceID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trace_id", err.Error())
		return
	}
	tr, err := s.Deps.Store.GetTrace(r.Context(), subject.Tenant, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "trace_not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// --- GET /v1/topology (screen: Topology) ----------------------------------

func (s *HTTPServer) handleTopology(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Topology == nil {
		writeError(w, http.StatusServiceUnavailable, "topology_unavailable", "topology graph is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	win := windowFromQuery(r, time.Now())
	edges, err := s.Deps.Topology.Edges(r.Context(), subject.Tenant, win)
	if err != nil {
		writeError(w, http.StatusBadGateway, "topology_query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

// --- GET /v1/incidents (screen: Overview) ---------------------------------

func (s *HTTPServer) handleIncidents(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Anomaly == nil {
		writeError(w, http.StatusServiceUnavailable, "anomaly_unavailable", "anomaly grouper is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	incidents, err := s.Deps.Anomaly.ActiveIncidents(r.Context(), subject.Tenant)
	if err != nil {
		writeError(w, http.StatusBadGateway, "incidents_query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
}

// --- GET /v1/investigations/{id} (screen: Investigations) ----------------

func (s *HTTPServer) handleInvestigationGet(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Investigations == nil {
		writeError(w, http.StatusServiceUnavailable, "rca_unavailable", "investigation engine is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	inv, err := s.Deps.Investigations.Get(r.Context(), subject.Tenant, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "investigation_not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// --- POST /v1/investigations/{id}/replay?mode= (FR-F12-12, DR-18) --------

func (s *HTTPServer) handleInvestigationReplay(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Investigations == nil {
		writeError(w, http.StatusServiceUnavailable, "rca_unavailable", "investigation engine is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = string(model.ReplayRecorded)
	}
	var rm model.ReplayMode
	switch mode {
	case string(model.ReplayRecorded):
		rm = model.ReplayRecorded
	case string(model.ReplayLiveDiff):
		rm = model.ReplayLiveDiff
	default:
		writeError(w, http.StatusBadRequest, "invalid_mode", "mode must be recorded or live-diff")
		return
	}
	inv, err := s.Deps.Investigations.Replay(r.Context(), subject.Tenant, r.PathValue("id"), rm)
	if err != nil {
		writeError(w, http.StatusBadGateway, "replay_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, inv)
}

// --- POST /v1/investigations/{id}/steps/{stepID}/correct -----------------

type correctRequest struct {
	Reason   string `json:"reason"`
	NewValue string `json:"new_value"`
}

func (s *HTTPServer) handleInvestigationCorrect(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Investigations == nil {
		writeError(w, http.StatusServiceUnavailable, "rca_unavailable", "investigation engine is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	var body correctRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	c := model.Correction{
		TargetID:    r.PathValue("stepID"),
		Reason:      body.Reason,
		NewValue:    body.NewValue,
		CorrectedBy: subject.ID,
		CorrectedAt: time.Now(),
	}
	inv, err := s.Deps.Investigations.Correct(r.Context(), subject.Tenant, r.PathValue("id"), r.PathValue("stepID"), c)
	if err != nil {
		writeError(w, http.StatusBadGateway, "correct_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// --- GET /v1/remediation/actions (screen: Remediation) --------------------

func (s *HTTPServer) handleActionsList(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Remediate == nil {
		writeError(w, http.StatusServiceUnavailable, "remediate_unavailable", "remediation guard is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	f := remediate.ActionFilter{
		IncidentID: r.URL.Query().Get("incident_id"),
		Cursor:     r.URL.Query().Get("cursor"),
		Limit:      queryInt(r, "limit", 50),
	}
	actions, next, err := s.Deps.Remediate.List(r.Context(), subject.Tenant, f)
	if err != nil {
		writeError(w, http.StatusBadGateway, "actions_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "next_page_token": next})
}

// --- POST /v1/actions (FR-F12-19's Guard-reconstructed-payload boundary) -
//
// Proposes only — NEVER auto-executes (per the task brief: "propose/
// approve — NOT auto-execute").

type proposeActionRequest struct {
	IncidentID      string                        `json:"incident_id"`
	InvestigationID string                        `json:"investigation_id"`
	Type            model.ActionType              `json:"type"`
	Target          model.ActionTarget            `json:"target"`
	Rationale       string                        `json:"rationale"`
	Rollback        *model.RollbackDeploymentSpec `json:"rollback,omitempty"`
	Scale           *model.ScaleReplicasSpec      `json:"scale,omitempty"`
	Restart         *model.RestartPodSpec         `json:"restart,omitempty"`
	FeatureFlag     *model.ToggleFeatureFlagSpec  `json:"feature_flag,omitempty"`
	IstioFault      *model.RemoveIstioFaultSpec   `json:"istio_fault,omitempty"`
}

func (s *HTTPServer) handleActionsPropose(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Remediate == nil {
		writeError(w, http.StatusServiceUnavailable, "remediate_unavailable", "remediation guard is not configured")
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if idem == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	subject, _ := subjectFrom(r.Context())
	var body proposeActionRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	proposal := model.ActionProposal{
		IncidentID:      body.IncidentID,
		InvestigationID: body.InvestigationID,
		Type:            body.Type,
		Target:          body.Target,
		Rationale:       body.Rationale,
		ProposedBy:      subject.ID,
		ProposedAt:      time.Now(),
		Rollback:        body.Rollback,
		Scale:           body.Scale,
		Restart:         body.Restart,
		FeatureFlag:     body.FeatureFlag,
		IstioFault:      body.IstioFault,
	}
	action, err := s.Deps.Remediate.Propose(r.Context(), subject.Tenant, proposal, subject, idem)
	if err != nil {
		writeError(w, http.StatusBadGateway, "propose_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, action)
}

// --- POST /v1/actions/{id}/approve ----------------------------------------

type approveActionRequest struct {
	Comment               string `json:"comment"`
	OverrideJustification string `json:"override_justification"`
	RequestBudgetOverride bool   `json:"request_budget_override"`
}

func (s *HTTPServer) handleActionsApprove(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Remediate == nil {
		writeError(w, http.StatusServiceUnavailable, "remediate_unavailable", "remediation guard is not configured")
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if idem == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	subject, _ := subjectFrom(r.Context())
	var body approveActionRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	action, err := s.Deps.Remediate.Approve(r.Context(), subject.Tenant, r.PathValue("id"), subject, remediate.ApprovalRequest{
		Comment:               body.Comment,
		OverrideJustification: body.OverrideJustification,
		RequestBudgetOverride: body.RequestBudgetOverride,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, action)
}

// --- POST /v1/actions/{id}/rollback ---------------------------------------

type rollbackActionRequest struct {
	Reason string `json:"reason"`
}

func (s *HTTPServer) handleActionsRollback(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Remediate == nil {
		writeError(w, http.StatusServiceUnavailable, "remediate_unavailable", "remediation guard is not configured")
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if idem == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	subject, _ := subjectFrom(r.Context())
	var body rollbackActionRequest
	_ = decodeJSON(r, &body)
	action, err := s.Deps.Remediate.Rollback(r.Context(), subject.Tenant, r.PathValue("id"), subject, body.Reason)
	if err != nil {
		writeError(w, http.StatusBadGateway, "rollback_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, action)
}

// --- POST /v1/nl/query (screen: Chat) -------------------------------------

type nlQueryRequest struct {
	Text string `json:"text"`
}

func (s *HTTPServer) handleNLQuery(w http.ResponseWriter, r *http.Request) {
	if s.Deps.Interpreter == nil || s.Deps.Answerer == nil {
		writeError(w, http.StatusServiceUnavailable, "nl_unavailable", "NL interpreter/answerer is not configured")
		return
	}
	subject, _ := subjectFrom(r.Context())
	var body nlQueryRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	question := nl.Question{Text: body.Text, Subject: subject, AskedAt: time.Now()}
	cc := nl.ConversationContext{TenantID: subject.Tenant, UpdatedAt: time.Now()}
	intent, err := s.Deps.Interpreter.Interpret(r.Context(), subject.Tenant, question, cc)
	if err != nil {
		writeError(w, http.StatusBadGateway, "interpret_failed", err.Error())
		return
	}
	answer, err := s.Deps.Answerer.Answer(r.Context(), subject.Tenant, intent, cc)
	if err != nil {
		writeError(w, http.StatusBadGateway, "answer_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// --- GET /healthz, /readyz (FR-F12's availability NFR) --------------------

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *HTTPServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
