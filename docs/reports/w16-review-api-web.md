# w16 — Review: internal/api + web/ (F12 UI/API/Integrations)

Binding sources read: `docs/architecture/features/F12-ui-api-integrations.md` (full), DR-25
(L2595-2740, auth), DR-26 (L2741-2822, listeners/TLS/limits), DR-29 (L2941-3000, API surface/MCP),
DR-30 (L3001-3037, UI screens). Prior work reviewed: `docs/reports/w15-api-web.md`.

## Verdict: **APPROVE-WITH-FIXES**

One Major finding (MCP JSON-Schema validation, FR-F12-6) was found and **fixed** in this pass.
No Blockers. No RBAC fail-open bug of the wave-14 `nl` class exists anywhere in `internal/api`.
Two Minor findings and one pre-existing, out-of-scope flaky test are documented below, not fixed.

## 1. Spec compliance vs F12's endpoint table and MCP tool set

### REST endpoints (F12 §4.3 / DR-29 §29.1)

Counting every concrete method+path in F12 §4.3's table (grouped rows expanded: 4 Grafana routes,
3 identity routes, 2 eval routes, 3 store/sampler routes, 2 health routes) = **34 endpoints**.

**16/34 implemented (47%)** — matches the w15 report's own "roughly half" claim; verified by
re-deriving the count independently rather than trusting the prior report's arithmetic:

Implemented: `GET /healthz`, `GET /readyz`, `GET /v1/overview`, `GET /v1/traces`,
`GET /v1/traces/{id}`, `GET /v1/topology`, `GET /v1/incidents`, `GET /v1/investigations/{id}`,
`POST /v1/investigations/{id}/replay`, `POST /v1/investigations/{id}/steps/{stepID}/correct`,
`GET /v1/remediation/actions`, `POST /v1/actions`, `POST /v1/actions/{id}/approve`,
`POST /v1/actions/{id}/rollback`, `POST /v1/nl/query`, `POST /v1/mcp`.

Not implemented (all correctly absent from the mux, not faked): `GET /v1/topology/snapshot`,
`GET /v1/audit?verify=`, `GET/POST /v1/identities`, `DELETE /v1/identities/{id}`,
`GET /v1/eval/runs*`, `GET /v1/store/budget`, `GET /v1/sampler/*`, `GET /v1/tenants/{id}/erasure`,
`GET /grafana*` (4 routes), Slack/PagerDuty webhooks (2 routes), `GET /metrics`.

Every implemented route's role in `internal/api/server.go`'s `routes()` matches its F12 §4.3 row
(cross-checked each `requireCapability(Cap..., ...)` call against the table by hand — no mismatches
found).

### MCP tools (F12 §4.3 / DR-29 §29.2, 12 tools)

**3/12 dispatch real logic** (`traceiq_search_traces`, `traceiq_get_trace`,
`traceiq_topology_neighbors`), **9/12 are documented stubs** returning JSON-RPC `-32002` — never a
faked success, confirmed by reading `mcp.go`'s `implementedTools` map and the `default:` arm of
`mcpToolsCall`'s switch. `traceiq_propose_action` is correctly absent (FR-F12-6). `tools/list`
returns all 12 with an `implemented: bool` flag (`TestMCP_ToolsList`, passing).

## 2. RBAC enforcement — explicit fail-open check

**Result: no fail-open bug found.** Specifically checked every code path that could skip an
authz check when the authorizer is unset (the wave-14 `nl` class of bug):

- `middleware.go`'s `authenticate()`: `s.Deps.Auth == nil` → `401`, never a pass-through.
- `middleware.go`'s `requireCapability()`: `s.Deps.Authorizer == nil` → `403`, never a
  pass-through. Every REST route in `server.go`'s `routes()` except `/healthz`, `/readyz`
  (deliberately unauthenticated per F12 §4.3) and `/v1/mcp` (see below) is wrapped in
  `requireCapability`.
- `mcp.go`'s `mcpToolsCall()`: `s.Deps.Authorizer == nil` → JSON-RPC `-32001` unauthorized, checked
  **before** dispatch and before the not-implemented check, for every tool call. `/v1/mcp` itself
  is wrapped in `authenticate()` (401 on nil `Auth`), then re-checks the caller's Subject inside
  `handleMCP` (`subjectFrom` ok-check) before doing anything else.

No handler in `internal/api` reaches business logic without going through one of these three gates.
`TestOverview_UnauthorizedCallerRejected`, `TestActionsPropose_OperatorAllowed_ViewerDenied`,
`TestActionsApprove_RBAC`, `TestMCP_UnauthenticatedRejected`, and
`TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented` all pass and exercise these
gates against the real `fakeAuthorizer` capability matrix (`internal/api/testutil_test.go`), which
itself correctly implements DR-25 §25.2's separation of duty (approver has no
`remediation_action:propose`; operator has no `remediation_action:approve`).

**Test-quality note (Minor, not fixed):** explicit 403-for-unauthorized-caller tests exist only for
`GET /v1/overview` and the remediation-actions flow. `Traces`, `Topology`, `Investigations`, and
`NL query` endpoints have only "allowed" tests — correctness here was confirmed by code reading
(they share the same `requireCapability` wrapper), not by a dedicated test per endpoint. Recommend
a table-driven RBAC-denial test iterating `server.go`'s route table in a follow-up.

## 3. `topology_adapter.go` + `LiveGraph.Has()` — verified correct

- **Tenant scoping**: `LiveGraph.Has` (`internal/topology/livegraph.go:359`) does a two-level
  lookup, `g.services[tid][service]`, under `RLock` — a service seen for tenant A is correctly
  invisible when queried for tenant B. Confirmed by reading the same `ensureService` write path
  that populates `g.services`, which is itself keyed by `tid` first.
- **`dir uint8 ↔ topology.Direction` translation**: `topology.Direction` constants
  (`internal/topology/topology.go:51-56`) are `Upstream=1, Downstream=2, Both=3`;
  `anomaly.TopologyReader.Neighbors`'s `dir uint8` parameter (`internal/anomaly/anomaly.go:48`) is
  called from exactly one call site (`internal/anomaly/grouper.go:141`) with the literal `3`. A
  direct numeric cast is therefore lossless for the only value ever passed, in the only direction
  the adapter performs the cast (`uint8 -> Direction`, in `Neighbors`); no code path does the
  reverse cast. This is a narrow-but-correct fix, not a coincidence — verified by grep, not by
  reading the report's own claim.
- **`Neighborhood` → flat `[]string` merge**: `Neighbors` in `topology_adapter.go` merges
  `Upstream`+`Downstream` into one deduplicated slice, matching `anomaly/grouper_test.go`'s
  `fakeTopo` stub's existing flat-set semantics — consistent, not a silent behavior change for
  `anomaly.Grouper`'s only consumer.

No misreporting found in either method.

## 4. SearchSpans fix — MCP `search_traces` verified end-to-end, not passing by accident

Re-ran `TestMCP_SearchTraces_RealStore` in isolation (`-run` + `-count=1`, no cache): **PASS**.
Read the fix in `internal/store/sqlite/hotindex.go:287-309` (the `q.Service != ""` branch of
`SearchSpans`): the bug was that `Operation`/`Window` filters were applied unconditionally even
when unset, which zeroed out every row for a `Service`-only query (exactly `mcpSearchTraces`'s call
shape, `store.SpanQuery{Service, Limit}` — no `Operation`, no `Window`). The fix makes both filters
conditional on being actually set. The test asserts `len(page.Spans) == 1` against a trace written
through the real `WriteBatch` path — a specific, falsifiable assertion, not a vacuous pass. Also ran
the full `internal/api` suite (28 tests after this pass's additions, see §6) and the full
`internal/store/sqlite` suite — both green.

## 5. Embedded `web/` SPA

- **`GET /` serves real HTML**: `TestStaticRoot_ServesRealHTML` asserts a non-empty `Content-Type`
  and a body containing `"TraceIQ"` — re-ran and confirmed passing; also manually read
  `web/static/index.html` and confirmed it is the real page (brand header, nav, `#view` mount
  point, `<script type="module" src="/js/app.js">`), not a placeholder.
- **XSS review**: read all four JS files (`app.js`, `overview.js`, `traces.js`, `api.js`). Every
  server-derived string interpolated into `innerHTML` (`overview.js`'s incident title/status/
  severity/score, `traces.js`'s root service/keep reason) goes through a local `escapeHTML()`
  helper first. No `eval`, no `innerHTML` assignment of raw unescaped data found. `api.js` uses
  `fetch` with `credentials: "same-origin"` only — no cross-origin credential leakage.
- **Secrets**: no hardcoded API keys, tokens, or credentials in any `web/static/**` file.
- **Gap (Minor, not fixed)**: F12 §6 requires
  `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'` on every
  response; `internal/api` sets no security headers anywhere (`httpjson.go`'s `writeJSON` only sets
  `Content-Type`; `mountStatic` sets none). Low risk today given the escaping above and the
  same-origin-only script tag, but it's a real, currently-unmet normative requirement, not
  currently tracked in the w15 report's own deferred-items list. Left as a follow-up rather than
  fixed here: a CSP header applies globally to every future screen this wave didn't build yet, and
  picking the right header now without those screens risks getting it wrong for them.

## 6. Correctness bugs found and fixed

### Major — FR-F12-6's MCP JSON-Schema validation was entirely absent (FIXED)

F12 §4.2 declares `MCPTool.Schema() json.RawMessage` and §4.4's algorithm requires
`validateAgainstSchema(call.arguments, tool.Schema())` before dispatch. The actual `api.MCPTool`
type (`api.go`) was a plain struct with **no `Schema` field at all**, and `mcpToolsCall` in `mcp.go`
never validated incoming arguments against anything — a malformed or incomplete argument object for
an implemented tool (e.g. `traceiq_get_trace` with no `trace_id`) would reach
`json.Unmarshal(raw, &args)` inside the tool's own handler and surface as a generic internal error
rather than the spec's `-32602`. This was not disclosed as a deferred item in the w15 report.

**Fix applied** (`internal/api/api.go`, `internal/api/mcp.go`):
- Added `Schema json.RawMessage` to `MCPTool` and a literal minimal JSON Schema (type/properties/
  required) for all 12 tools, including the 9 stubs (documents the contract even where it can't yet
  be exercised).
- Added `validateArgsAgainstSchema` (`mcp.go`): enforces the schema's `required` list only (not
  full type/format checking — a general JSON Schema validator is out of proportion for this fix;
  wrong-typed-but-present fields are still caught by each tool's own typed `json.Unmarshal`, which
  already returned clear errors).
- Wired into `mcpToolsCall` **after** the RBAC gate and the not-implemented gate, **before**
  dispatch: ordering it after not-implemented means a caller hitting a stub tool with bad args still
  gets "not implemented" (more useful, and avoids disturbing the existing
  `TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented`/
  `TestMCP_UnimplementedTool_ReturnsError` tests, which deliberately call stub tools with empty
  args); ordering it after RBAC preserves the existing "unauthorized caller learns nothing" property
  documented in `mcp.go`.
- Added 3 new tests: `TestMCP_GetTrace_MissingRequiredField_RejectedBeforeDispatch`,
  `TestMCP_TopologyNeighbors_MissingRequiredField_RejectedBeforeDispatch`,
  `TestMCP_SearchTraces_NoRequiredFields_EmptyArgsStillDispatches` (the last one guards against the
  fix over-rejecting a tool with no required fields).

**Re-verified**: `go build`/`go vet`/`go test -v` on `internal/api` all green (28/28 tests, up from
25), full-repo `go test ./...` green. See §7 for the full run.

## 7. Pre-existing, out-of-scope: flaky `internal/topology` test (not fixed)

`go test ./internal/topology/...` intermittently fails `TestEvictionAtCap` (~1 in 3 runs observed:
`want EdgesEvicted=1, got 2`). Root-caused by reading `evictIfNeeded` (`livegraph.go:278-294`): it
breaks eviction ties (`e.Calls < victim.Calls`, strictly-less) using Go's nondeterministic map
iteration order, and the test's third edge is built via two separate `Consume` calls whose first
call can tie the second edge's call count — depending on map order, either edge may be evicted, and
in the unlucky ordering the freshly-inserted edge gets evicted and immediately re-triggers a second
eviction check on its next `Consume`. This is **not caused by this wave's changes**: `Has()` and the
`topology_adapter.go` `Neighbors` translation never touch `evictIfNeeded` or the edge map at all —
confirmed by reading both code paths. This is pre-existing wave-10 code, and per the w15 report's
own framing, `internal/topology` is "separately reviewed" and out of this review's scope (which is
`internal/api` + `web/`, plus specifically verifying the two additive touch points into topology).
Flagged here rather than fixed, matching how the w15 report itself handled the (then out-of-scope)
`sqlite` bug — flag, don't silently patch someone else's reviewed package under time pressure.
Recommend a follow-up: fix the tie-break to be deterministic (e.g. prefer the oldest `FirstSeen` on
a tie) rather than relying on map iteration order.

## Full verification run (this pass, GOTOOLCHAIN=go1.27.1, CGO_ENABLED=0)

```
go build ./internal/api/... ./internal/topology/...   — exit 0
go vet   ./internal/api/... ./internal/topology/...    — exit 0
go test  ./internal/api/... ./internal/topology/... -v — internal/api: 28/28 PASS
                                                          internal/topology: PASS this run (flaky, see §7)
go build ./...                                          — exit 0
go vet   ./...                                          — exit 0
go test  ./...                                          — all packages PASS (internal/topology flaky, unrelated to this wave)
```

Files touched this pass: `internal/api/api.go` (added `MCPTool.Schema` + 12 schema literals),
`internal/api/mcp.go` (added `validateArgsAgainstSchema` + wired into `mcpToolsCall`),
`internal/api/mcp_test.go` (3 new tests). Nothing in `internal/topology`, `internal/store`, `web/`,
or any other package was touched.
