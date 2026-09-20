package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"traceiq/internal/anomaly"
	"traceiq/internal/auth"
	"traceiq/internal/cluster"
	"traceiq/internal/config"
	"traceiq/internal/correlate"
	"traceiq/internal/eval"
	"traceiq/internal/k8s"
	"traceiq/internal/memory"
	"traceiq/internal/nl"
	"traceiq/internal/rca"
	"traceiq/internal/remediate"
	"traceiq/internal/sampler"
	"traceiq/internal/selfobs"
	"traceiq/internal/store"
	"traceiq/internal/tenant"
	"traceiq/internal/topology"
)

// MCPTool is DR-29 §29.2's per-tool authorization record: "Authorization is
// per tool, through api.MCPTool.MinRole (which 02 §4 already declares and
// F12 omitted)". MinRole is auth.Role directly (DR-4: reference, never
// redefine); an MCP token defaults to viewer scope.
//
// Schema is FR-F12-6's "published JSON Schema" for the tool's `arguments`
// object ("each validating its parameters against a published JSON Schema
// before dispatch"). It is a real (if minimal — draft-07-shaped, `type`/
// `properties`/`required` only) schema for every tool, including the 9
// stubs, since the doc requirement is per-tool and does not carve out an
// exception for unimplemented tools; mcp.go's validateArgsAgainstSchema
// enforces only the `required` list (REVIEW FIX, w16: this was previously
// absent entirely — no Schema field existed and mcpToolsCall never
// validated params at all, a real FR-F12-6 gap found in review, not merely
// a cosmetic one, since a caller's malformed args reached tool.Call() and
// surfaced as a generic internal error rather than -32602. See
// docs/reports/w16-review-api-web.md).
type MCPTool struct {
	Name        string
	BackingCall string // documentation only, e.g. "store.SearchSpans"
	MinRole     auth.Role
	Schema      json.RawMessage
}

// MCPTools is DR-29 §29.2's closed twelve-tool set, verbatim: "01 §6.2's
// thirteen minus one" (traceiq_propose_action is not exposed over MCP in
// v1 — "an approval must name a person").
var MCPTools = [12]MCPTool{
	{Name: "traceiq_search_traces", BackingCall: "store.SearchSpans", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"errors_only":{"type":"boolean"},"limit":{"type":"integer"}},"required":[]}`)},
	{Name: "traceiq_get_trace", BackingCall: "store.GetTrace", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"trace_id":{"type":"string"}},"required":["trace_id"]}`)},
	{Name: "traceiq_query_red", BackingCall: "store.QueryRED", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"operation":{"type":"string"}},"required":["service"]}`)},
	{Name: "traceiq_topology_neighbors", BackingCall: "topology.Neighbors", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"hops":{"type":"integer"},"direction":{"type":"string"}},"required":["service"]}`)},
	{Name: "traceiq_topology_edges", BackingCall: "topology.Edges", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"window":{"type":"object"}},"required":[]}`)},
	{Name: "traceiq_list_incidents", BackingCall: "incident query", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`)},
	{Name: "traceiq_get_investigation", BackingCall: "investigation + steps + evidence", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"investigation_id":{"type":"string"}},"required":["investigation_id"]}`)},
	{Name: "traceiq_search_memory", BackingCall: "memory.Similar", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)},
	{Name: "traceiq_correlate_logs", BackingCall: "correlate.LogsForTrace", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"trace_id":{"type":"string"}},"required":["trace_id"]}`)},
	{Name: "traceiq_correlate_metrics", BackingCall: "correlate.MetricsForSpan", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"trace_id":{"type":"string"},"span_id":{"type":"string"}},"required":["trace_id","span_id"]}`)},
	{Name: "traceiq_ask", BackingCall: "nl.Answerer", MinRole: auth.RoleViewer,
		Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)},
	{Name: "traceiq_start_investigation", BackingCall: "rca.Engine.Investigate", MinRole: auth.RoleOperator,
		Schema: json.RawMessage(`{"type":"object","properties":{"incident_id":{"type":"string"}},"required":["incident_id"]}`)},
}

// Route is one row of 01 §6.1's endpoint table. DR-29 §29.1 adds the rows
// below to that table and corrects several existing roles; the full table
// lives in 01 §6.1 (out of this package's binding-read scope) and is not
// reproduced verbatim here.
// TODO(DR-29): populate the remainder of 01 §6.1's table once that doc
// section is read under a future DR pass.
type Route struct {
	Method string
	Path   string
	Role   auth.Role
}

// Routes is DR-29 §29.1's "Rows added to 01 §6.1" table, verbatim subset.
var Routes = []Route{
	{Method: "POST", Path: "/v1/identities", Role: auth.RoleAdmin},
	{Method: "GET", Path: "/v1/identities", Role: auth.RoleAdmin},
	{Method: "DELETE", Path: "/v1/identities/{id}", Role: auth.RoleAdmin},
	{Method: "GET", Path: "/v1/sampler/interest", Role: auth.RoleViewer},
	{Method: "DELETE", Path: "/v1/sampler/interest/{id}", Role: auth.RoleOperator},
	{Method: "GET", Path: "/v1/sampler/stats", Role: auth.RoleViewer},
	{Method: "GET", Path: "/v1/store/budget", Role: auth.RoleViewer},
	{Method: "GET", Path: "/v1/baselines", Role: auth.RoleViewer},
	{Method: "GET", Path: "/v1/deploys", Role: auth.RoleViewer},
	{Method: "GET", Path: "/v1/tenants/{id}/erasure", Role: auth.RoleAdmin},
	{Method: "POST", Path: "/v1/actions/{id}/rollback", Role: auth.RoleApprover},
	{Method: "GET", Path: "/v1/audit", Role: auth.RoleAdmin}, // corrected from viewer (DR-29 §29.1)
	{Method: "POST", Path: "/v1/eval/runs", Role: auth.RoleAdmin},
	{Method: "POST", Path: "/v1/investigations/{id}/replay", Role: auth.RoleOperator},
	{Method: "POST", Path: "/v1/investigations/{id}/steps/{stepID}/correct", Role: auth.RoleOperator},
	{Method: "POST", Path: "/v1/mcp", Role: auth.RoleViewer}, // per-tool gate, DR-29 §29.2
}

// Deps is DR-30's "api.Server interface + api.HTTPServer impl + api.Deps
// wiring", matching 02 §4's shape (reconciled in the round-2 fix wave, N2-4:
// 02 §4's api_Deps previously predated DR-6/DR-7/DR-3/DR-14's interface
// splits and named an anomaly_Engine type that exists in no DR and no Go
// package; 02 §4 was corrected to this struct's shape rather than the
// reverse — see 02 §4's N2-4 note). It carries the interfaces every handler
// group needs; api is the sole package permitted to import every feature
// package (DR-2), so each field below is a directly usable interface
// rather than a further indirection.
//
// The register never prints an explicit `type Deps struct` block, so this
// shape is reconstructed from what DR-29 §29.1's route table, DR-29 §29.2's
// MCP backing-call table and DR-30's per-screen endpoint list actually
// require api.HTTPServer to hold, one field per interface a handler group
// dispatches to — the same pattern the pre-existing Auth*/Tenants/Policies
// fields already follow. rca, memory, correlate, anomaly, sampler and store
// were out of scope for the pass that first wrote this struct (see
// docs/reports/w2-scaffold-c.md); their fields are filled in below now that
// those packages are populated.
//
// internal/ingest is deliberately NOT wired here: DR-29/DR-30 name no
// ingest-backed route or MCP tool, and ingest.Receiver is a per-protocol
// process-lifecycle handle (Start/Stop/Protocol), not a query surface an
// HTTP handler would call — cmd/traceiq holds it directly instead.
type Deps struct {
	Config  *config.Config
	SelfObs selfobs.Registry
	Cluster cluster.Elector

	Auth       auth.Authenticator
	Authorizer auth.Authorizer
	RateLimit  auth.RateLimiter
	Audit      auth.AuditSink
	Identities auth.IdentityStore

	Tenants  tenant.Resolver
	Policies tenant.PolicyStore

	Topology  topology.Graph
	Remediate remediate.Guard
	// Interpreter and Answerer are both required (N2-4 fix): DR-35 §35.2
	// keeps nl.Interpreter and nl.Answerer as two interfaces, and
	// Answerer.Answer takes the Intent that only Interpreter.Interpret
	// produces, so a /v1/ask handler needs both steps, not just the second.
	Interpreter nl.Interpreter
	Answerer    nl.Answerer
	Eval        eval.Runner

	// Investigations backs traceiq_get_investigation/traceiq_start_investigation
	// (DR-29 §29.2) and the /v1/investigations/{id}/replay and
	// /v1/investigations/{id}/steps/{stepID}/correct rows (DR-29 §29.1).
	Investigations rca.Engine

	// Memory backs traceiq_search_memory (DR-29 §29.2) and screen 9 (DR-30).
	Memory memory.Store

	// Correlate backs traceiq_correlate_logs/traceiq_correlate_metrics
	// (DR-29 §29.2).
	Correlate correlate.Correlator

	// Anomaly backs traceiq_list_incidents (DR-29 §29.2) and the
	// incidents/anomalies panels of screen 1 (DR-30).
	Anomaly anomaly.Grouper
	// Baselines backs GET /v1/baselines (DR-29 §29.1; DR-30 screen 1/8).
	Baselines anomaly.BaselineReader
	// Deploys backs GET /v1/deploys (DR-29 §29.1; DR-30 screen 1).
	Deploys anomaly.DeployIndex

	// Sampler backs GET/DELETE /v1/sampler/interest and GET /v1/sampler/stats
	// (DR-29 §29.1; DR-30 screen 8's Sampler panel).
	Sampler sampler.Sampler

	// Store backs traceiq_search_traces/traceiq_get_trace/traceiq_query_red
	// (DR-29 §29.2) and GET /v1/store/budget (DR-29 §29.1).
	Store store.HotIndex
	// Cold backs GET /v1/tenants/{id}/erasure (DR-29 §29.1; DR-7's tombstone
	// SLA).
	Cold store.ColdStore

	K8s k8s.Executor
}

// Server is DR-30's CC-24 rename target's interface half: "api.Server
// interface + api.HTTPServer impl". The register does not print its method
// set explicitly beyond the rename note; reconstructed minimally as the
// process lifecycle every server.mode needs.
// TODO(DR-30): confirm method set against 02 §4's full Server shape.
type Server interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Addr() string
}

// HTTPServer is DR-30's CC-24 rename: "F12 §4.2's `type Server struct` is
// renamed api.HTTPServer and its inline dependency fields are replaced by
// api.Deps". This is what lets server.mode: api be wired without brain
// components.
type HTTPServer struct {
	Deps Deps

	addr string
	mux  *http.ServeMux

	mu  sync.Mutex
	ln  net.Listener
	srv *http.Server
}

var _ Server = (*HTTPServer)(nil)
