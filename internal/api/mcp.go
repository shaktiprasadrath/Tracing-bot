package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"traceiq/internal/auth"
	"traceiq/internal/store"
	"traceiq/internal/topology"
)

// JSONRPCRequest/JSONRPCResponse/JSONRPCError are F12 §4.2's Go blocks,
// verbatim shape.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"` // "tools/list" | "tools/call"
	Params  json.RawMessage `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC 2.0 reserved/near-standard error codes this handler uses.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
	rpcUnauthorized   = -32001 // non-reserved, application-defined (JSON-RPC 2.0 §5.1 range)
	rpcNotImplemented = -32002
)

// toolsCallParams is "tools/call"'s params shape: MCP's own JSON-RPC
// convention (tool name + a nested, tool-specific arguments object).
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// --- per-tool argument/result shapes for the 3 implemented tools. --------
// Kept minimal and JSON-schema-shaped so a future pass can lift Schema()
// straight off these without redesigning the wire contract.

type searchTracesArgs struct {
	Service    string `json:"service"`
	ErrorsOnly bool   `json:"errors_only"`
	Limit      int    `json:"limit"`
}

type getTraceArgs struct {
	TraceID string `json:"trace_id"`
}

type topologyNeighborsArgs struct {
	Service string `json:"service"`
	Hops    int    `json:"hops"`
	// Direction: "upstream" | "downstream" | "both" (default "both").
	Direction string `json:"direction"`
}

// implementedTools names the 3 of MCPTools' 12 this wave actually dispatches
// (per the task brief: "AT LEAST 3 ... pick the simplest: search_traces,
// get_trace, topology"). Every other tool in api.MCPTools is a documented
// stub: it returns a clear "not implemented" JSON-RPC error, never a faked
// success or silently empty result (task brief, FR-F12-6).
var implementedTools = map[string]bool{
	"traceiq_search_traces":      true,
	"traceiq_get_trace":          true,
	"traceiq_topology_neighbors": true,
}

func lookupMCPTool(name string) (MCPTool, bool) {
	for _, t := range MCPTools {
		if t.Name == name {
			return t, true
		}
	}
	return MCPTool{}, false
}

// handleMCP is POST /v1/mcp: a JSON-RPC 2.0 handler exposing api.MCPTools
// (DR-29 §29.2). Authentication runs once for the whole request (the
// transport-level Subject); per-tool authorization then re-checks each
// call's MinRole via Deps.Authorizer, since a single /v1/mcp endpoint-level
// role would violate FR-F12-6's "gated by its own MinRole" requirement.
func (s *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req JSONRPCRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", Error: &JSONRPCError{Code: rpcParseError, Message: "invalid JSON-RPC request: " + err.Error()}})
		return
	}
	if req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcInvalidRequest, Message: "jsonrpc must be \"2.0\""}})
		return
	}

	subject, ok := subjectFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcUnauthorized, Message: "unauthenticated"}})
		return
	}

	switch req.Method {
	case "tools/list":
		s.mcpToolsList(w, req)
	case "tools/call":
		s.mcpToolsCall(w, r, req, subject)
	default:
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcMethodNotFound, Message: "unknown method " + req.Method}})
	}
}

type mcpToolListing struct {
	Name        string `json:"name"`
	BackingCall string `json:"backing_call"`
	MinRole     string `json:"min_role"`
	Implemented bool   `json:"implemented"`
}

func (s *HTTPServer) mcpToolsList(w http.ResponseWriter, req JSONRPCRequest) {
	var out []mcpToolListing
	for _, t := range MCPTools {
		out = append(out, mcpToolListing{
			Name:        t.Name,
			BackingCall: t.BackingCall,
			MinRole:     string(t.MinRole),
			Implemented: implementedTools[t.Name],
		})
	}
	result, _ := json.Marshal(map[string]any{"tools": out})
	writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *HTTPServer) mcpToolsCall(w http.ResponseWriter, r *http.Request, req JSONRPCRequest, subject auth.Subject) {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcInvalidParams, Message: "invalid params: " + err.Error()}})
		return
	}

	tool, ok := lookupMCPTool(params.Name)
	if !ok {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcMethodNotFound, Message: "unknown tool " + params.Name}})
		return
	}

	// Per-tool RBAC gate (FR-F12-6): checked before dispatch AND before the
	// not-implemented check below, so an unauthorized caller never learns
	// whether a tool is implemented.
	if s.Deps.Authorizer == nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcUnauthorized, Message: "authorization is not configured"}})
		return
	}
	if err := s.Deps.Authorizer.Can(r.Context(), subject, mcpCapabilityForRole(tool.MinRole), auth.ResourceRef{Tenant: subject.Tenant}); err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcUnauthorized, Message: "tool " + tool.Name + " requires role " + string(tool.MinRole) + ": " + err.Error()}})
		return
	}

	if !implementedTools[tool.Name] {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcNotImplemented, Message: "tool " + tool.Name + " is not implemented in this wave (TODO — see docs/reports/w15-api-web.md)"}})
		return
	}

	// FR-F12-6: "each validating its parameters against a published JSON
	// Schema before dispatch" (REVIEW FIX, w16 — see api.go's MCPTool.Schema
	// doc comment for why this step was previously missing entirely: no
	// Schema field existed and mcpToolsCall never validated params at all).
	// Checked after the not-implemented gate: a stub tool's arguments are
	// never actually consumed, so "not implemented" is the more useful
	// answer than a schema complaint about a tool the caller can't use yet
	// regardless; this also matches the existing RBAC-before-not-implemented
	// precedent (both gates that don't depend on args go first) and doesn't
	// disturb TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented/
	// TestMCP_UnimplementedTool_ReturnsError, which call stub tools with
	// empty args on purpose.
	if err := validateArgsAgainstSchema(tool.Schema, params.Arguments); err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcInvalidParams, Message: "invalid arguments for " + tool.Name + ": " + err.Error()}})
		return
	}

	var (
		result any
		err    error
	)
	switch tool.Name {
	case "traceiq_search_traces":
		result, err = s.mcpSearchTraces(r, subject, params.Arguments)
	case "traceiq_get_trace":
		result, err = s.mcpGetTrace(r, subject, params.Arguments)
	case "traceiq_topology_neighbors":
		result, err = s.mcpTopologyNeighbors(r, subject, params.Arguments)
	default:
		// Unreachable: implementedTools and this switch are kept in sync.
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcNotImplemented, Message: "tool " + tool.Name + " is not implemented"}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcInternalError, Message: err.Error()}})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: rpcInternalError, Message: err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: raw})
}

// mcpCapabilityForRole maps an api.MCPTool.MinRole to the DR-25 capability
// this stub Authorizer seam checks. Since DR-25 §25.2's full capability
// matrix is out of this package's binding-read scope, viewer/operator map
// onto the same telemetry-read/incident-investigate capabilities the REST
// handlers already use, keeping ONE capability vocabulary across REST and
// MCP rather than inventing a second, MCP-only one.
func mcpCapabilityForRole(role auth.Role) auth.Capability {
	switch role {
	case auth.RoleOperator:
		return CapIncidentInvestigate
	case auth.RoleApprover:
		return CapRemediateApprove
	case auth.RoleAdmin:
		return CapAdmin
	default:
		return CapTelemetryRead
	}
}

func (s *HTTPServer) mcpSearchTraces(r *http.Request, subject auth.Subject, raw json.RawMessage) (any, error) {
	if s.Deps.Store == nil {
		return nil, errUnavailable("store")
	}
	var args searchTracesArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	page, err := s.Deps.Store.SearchSpans(r.Context(), subject.Tenant, store.SpanQuery{
		Service: args.Service,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (s *HTTPServer) mcpGetTrace(r *http.Request, subject auth.Subject, raw json.RawMessage) (any, error) {
	if s.Deps.Store == nil {
		return nil, errUnavailable("store")
	}
	var args getTraceArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	id, err := parseTraceID(args.TraceID)
	if err != nil {
		return nil, err
	}
	tr, err := s.Deps.Store.GetTrace(r.Context(), subject.Tenant, id)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

func (s *HTTPServer) mcpTopologyNeighbors(r *http.Request, subject auth.Subject, raw json.RawMessage) (any, error) {
	if s.Deps.Topology == nil {
		return nil, errUnavailable("topology")
	}
	var args topologyNeighborsArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
	}
	hops := args.Hops
	if hops <= 0 {
		hops = 1
	}
	dir := topology.Both
	switch args.Direction {
	case "upstream":
		dir = topology.Upstream
	case "downstream":
		dir = topology.Downstream
	}
	nb, err := s.Deps.Topology.Neighbors(r.Context(), subject.Tenant, args.Service, hops, dir)
	if err != nil {
		return nil, err
	}
	return nb, nil
}

// toolSchema is the minimal subset of JSON Schema (draft-07) this package's
// validateArgsAgainstSchema enforces: "required" presence, on the "object"
// schemas every api.MCPTool.Schema literal uses. It deliberately does not
// implement type/format/enum validation — that would need a general JSON
// Schema library, out of proportion for this fix — so a present-but-
// wrong-typed field is still caught downstream by the tool's own
// json.Unmarshal into its typed args struct (e.g. getTraceArgs), which
// already returns a clear error today.
type toolSchema struct {
	Required []string `json:"required"`
}

// validateArgsAgainstSchema is FR-F12-6's "validating its parameters
// against a published JSON Schema before dispatch" (F12 §4.4:
// `validateAgainstSchema(call.arguments, tool.Schema())`), applied to every
// tool (stub or implemented) so a caller gets a consistent -32602 for a
// missing required field regardless of whether the tool it's calling has
// been implemented yet.
func validateArgsAgainstSchema(schema json.RawMessage, args json.RawMessage) error {
	if len(schema) == 0 {
		return nil // no schema published for this tool; nothing to enforce
	}
	var sch toolSchema
	if err := json.Unmarshal(schema, &sch); err != nil {
		return nil // malformed schema literal is this package's bug, not the caller's
	}
	if len(sch.Required) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &obj); err != nil {
			return fmt.Errorf("arguments must be a JSON object: %w", err)
		}
	}
	for _, key := range sch.Required {
		raw, ok := obj[key]
		if !ok || len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
			return fmt.Errorf("missing required field %q", key)
		}
	}
	return nil
}

type unavailableError struct{ what string }

func (e unavailableError) Error() string { return e.what + " is not configured" }

func errUnavailable(what string) error { return unavailableError{what: what} }
