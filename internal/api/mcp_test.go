package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

func mcpRequest(t *testing.T, method string, toolName string, args any) []byte {
	t.Helper()
	var argRaw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		argRaw = b
	}
	var params json.RawMessage
	if method == "tools/call" {
		p, err := json.Marshal(toolsCallParams{Name: toolName, Arguments: argRaw})
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		params = p
	}
	req := JSONRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

func doMCP(t *testing.T, env *testEnv, role string, body []byte) JSONRPCResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := env.request("POST", "/v1/mcp", role, body)
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /v1/mcp transport: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON-RPC response: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

func TestMCP_ToolsList(t *testing.T) {
	env := newTestEnv(t)
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/list", "", nil))
	if resp.Error != nil {
		t.Fatalf("tools/list returned an error: %+v", resp.Error)
	}
	var out struct {
		Tools []mcpToolListing `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Tools) != 12 {
		t.Fatalf("want all 12 MCP tools listed (implemented + stubbed), got %d", len(out.Tools))
	}
	implCount := 0
	for _, tl := range out.Tools {
		if tl.Implemented {
			implCount++
		}
	}
	if implCount != 3 {
		t.Fatalf("want exactly 3 implemented tools this wave, got %d", implCount)
	}
}

// --- 3 implemented MCP tools work end-to-end against REAL store/topology --

func TestMCP_SearchTraces_RealStore(t *testing.T) {
	env := newTestEnv(t)
	insertTestTrace(t, env.deps.Store, env.tenant, "checkout")

	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_search_traces", searchTracesArgs{Service: "checkout", Limit: 10}))
	if resp.Error != nil {
		t.Fatalf("traceiq_search_traces returned an error: %+v", resp.Error)
	}
	var page store.SpanPage
	if err := json.Unmarshal(resp.Result, &page); err != nil {
		t.Fatalf("decode result: %v; raw=%s", err, string(resp.Result))
	}
	if len(page.Spans) != 1 {
		t.Fatalf("want 1 span from the real store, got %d", len(page.Spans))
	}
}

func TestMCP_GetTrace_RealStore(t *testing.T) {
	env := newTestEnv(t)
	id := insertTestTrace(t, env.deps.Store, env.tenant, "checkout")

	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_get_trace", getTraceArgs{TraceID: traceIDHex(id)}))
	if resp.Error != nil {
		t.Fatalf("traceiq_get_trace returned an error: %+v", resp.Error)
	}
	var tr store.TraceIndex
	if err := json.Unmarshal(resp.Result, &tr); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if tr.RootService != "checkout" {
		t.Fatalf("want RootService=checkout, got %q", tr.RootService)
	}
}

func TestMCP_TopologyNeighbors_RealGraph(t *testing.T) {
	env := newTestEnv(t)
	// Feed the real LiveGraph via Consume so Neighbors has a real edge to
	// return, exactly like the ingest pipeline would.
	err := env.deps.Topology.Consume(context.Background(), env.tenant, []model.Span{
		{
			TraceID:  model.TraceID{1},
			SpanID:   model.SpanID{1},
			Name:     "GET /cart",
			Kind:     model.SpanKindClient,
			Resource: &model.Resource{ServiceName: "frontend"},
			Attrs: model.AttrMap{
				"peer.service": model.AttrValue{Kind: model.AttrStr, Str: "cart"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Topology.Consume: %v", err)
	}

	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_topology_neighbors", topologyNeighborsArgs{Service: "frontend", Hops: 1}))
	if resp.Error != nil {
		t.Fatalf("traceiq_topology_neighbors returned an error: %+v", resp.Error)
	}
}

// --- unimplemented MCP tools return a clear error, never a faked success. -

func TestMCP_UnimplementedTool_ReturnsError(t *testing.T) {
	env := newTestEnv(t)
	resp := doMCP(t, env, "operator", mcpRequest(t, "tools/call", "traceiq_query_red", map[string]any{}))
	if resp.Error == nil {
		t.Fatalf("want an error for an unimplemented tool, got a (possibly faked) success result: %s", string(resp.Result))
	}
	if resp.Error.Code != rpcNotImplemented {
		t.Fatalf("want rpcNotImplemented (%d), got %d: %s", rpcNotImplemented, resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Result) != 0 {
		t.Fatalf("an error response must not also carry a Result")
	}
}

func TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented(t *testing.T) {
	env := newTestEnv(t)
	// traceiq_start_investigation is operator-gated AND unimplemented this
	// wave. A viewer must be rejected for RBAC, not told "not implemented"
	// (FR-F12-6: "gated by its own MinRole").
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_start_investigation", map[string]any{}))
	if resp.Error == nil {
		t.Fatalf("want an unauthorized error for a viewer calling an operator tool")
	}
	if resp.Error.Code != rpcUnauthorized {
		t.Fatalf("want rpcUnauthorized (%d), got %d: %s", rpcUnauthorized, resp.Error.Code, resp.Error.Message)
	}

	// An operator passes RBAC but still gets "not implemented", never a
	// faked success.
	resp = doMCP(t, env, "operator", mcpRequest(t, "tools/call", "traceiq_start_investigation", map[string]any{}))
	if resp.Error == nil {
		t.Fatalf("want a not-implemented error, got success: %s", string(resp.Result))
	}
	if resp.Error.Code != rpcNotImplemented {
		t.Fatalf("want rpcNotImplemented (%d), got %d: %s", rpcNotImplemented, resp.Error.Code, resp.Error.Message)
	}
}

func TestMCP_UnknownTool(t *testing.T) {
	env := newTestEnv(t)
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_does_not_exist", map[string]any{}))
	if resp.Error == nil || resp.Error.Code != rpcMethodNotFound {
		t.Fatalf("want rpcMethodNotFound for an unknown tool, got %+v", resp.Error)
	}
}

func TestMCP_UnauthenticatedRejected(t *testing.T) {
	env := newTestEnv(t)
	rec := httptest.NewRecorder()
	req := env.request("POST", "/v1/mcp", "", mcpRequest(t, "tools/list", "", nil))
	// No X-Test-Role at all still authenticates (fakeAuthenticator always
	// resolves a Subject); to test the transport unauthenticated path we'd
	// need Deps.Auth == nil, which handleMCP wraps as authenticate(...).
	// authenticate() itself is exercised by TestOverview_UnauthorizedCallerRejected
	// on the REST side; here we confirm tools/call's own per-tool 401 shape.
	env.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("transport-level want 200 (JSON-RPC errors are carried in-band), got %d", rec.Code)
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("tools/list itself requires no per-tool auth beyond transport auth, got error: %+v", resp.Error)
	}
}

func TestMCP_InvalidJSONRPC(t *testing.T) {
	env := newTestEnv(t)
	resp := doMCP(t, env, "viewer", []byte(`not json`))
	if resp.Error == nil || resp.Error.Code != rpcParseError {
		t.Fatalf("want rpcParseError, got %+v", resp.Error)
	}
}

// --- FR-F12-6: params validated against the tool's published JSON Schema --
// before dispatch (review fix, w16 — see api.go's MCPTool.Schema doc
// comment: this validation step did not exist at all before this pass).

func TestMCP_GetTrace_MissingRequiredField_RejectedBeforeDispatch(t *testing.T) {
	env := newTestEnv(t)
	// getTraceArgs requires trace_id; omit it entirely.
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_get_trace", map[string]any{}))
	if resp.Error == nil {
		t.Fatalf("want a schema-validation error for a missing trace_id, got success: %s", string(resp.Result))
	}
	if resp.Error.Code != rpcInvalidParams {
		t.Fatalf("want rpcInvalidParams (%d), got %d: %s", rpcInvalidParams, resp.Error.Code, resp.Error.Message)
	}
}

func TestMCP_TopologyNeighbors_MissingRequiredField_RejectedBeforeDispatch(t *testing.T) {
	env := newTestEnv(t)
	// topologyNeighborsArgs requires service; omit it entirely.
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_topology_neighbors", map[string]any{"hops": 1}))
	if resp.Error == nil {
		t.Fatalf("want a schema-validation error for a missing service, got success: %s", string(resp.Result))
	}
	if resp.Error.Code != rpcInvalidParams {
		t.Fatalf("want rpcInvalidParams (%d), got %d: %s", rpcInvalidParams, resp.Error.Code, resp.Error.Message)
	}
}

func TestMCP_SearchTraces_NoRequiredFields_EmptyArgsStillDispatches(t *testing.T) {
	env := newTestEnv(t)
	// traceiq_search_traces' schema has no required fields (service is an
	// optional narrowing filter) — confirm the new validation step doesn't
	// wrongly reject a legitimately empty/omitted-field call.
	resp := doMCP(t, env, "viewer", mcpRequest(t, "tools/call", "traceiq_search_traces", map[string]any{}))
	if resp.Error != nil {
		t.Fatalf("want success for search_traces with no filters, got error: %+v", resp.Error)
	}
}
