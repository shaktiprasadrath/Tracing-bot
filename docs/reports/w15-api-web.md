# w15 — internal/api + web/ (F12 UI/API/Integrations)

Binding sources: `docs/architecture/features/F12-ui-api-integrations.md` (full), DR-25 (auth),
DR-26 (§4.3 listener table — not consumed this wave, see below), DR-29 (§29.1 endpoint table,
§29.2 MCP tool table), DR-30 (Server/HTTPServer/Deps shape).

## Scope of this wave

`internal/api` had only `api.go`/`doc.go` (Route/MCPTool/Deps/Server/HTTPServer types, no
handlers, `Start`/`Shutdown`/`Addr` all `panic("not implemented")`). This wave implements:

- A real `net/http`-only HTTP server (`internal/api/server.go`, `handlers.go`, `mcp.go`,
  `middleware.go`, `httpjson.go`, `ids.go`) wired against real `store`/`topology`/`anomaly`/`rca`
  (rules reasoner only)/`memory`/`remediate`/`nl` implementations.
- `web/` (new package): a vanilla-ES-module SPA, `go:embed`'d, served from `internal/api`.
- Table-driven `httptest` tests in `internal/api/*_test.go`, wired against **real** components
  (sqlite-backed `store.HotIndex`, in-memory `topology.LiveGraph`/`anomaly.Grouper`/
  `memory.Store`/`rca.Engine`/`remediate.Guard`), never mocks, per the task brief.

## Endpoint coverage vs F12 §4.3's table

| Method & path | F12 role | Implemented | Notes |
|---|---|---|---|
| `GET /healthz`, `GET /readyz` | unauthenticated | yes | |
| `GET /v1/overview` | viewer | yes | folds `/v1/incidents` + per-service RED into one payload |
| `GET /v1/traces` | viewer | yes | `store.SearchTraces` |
| `GET /v1/traces/{id}` | viewer | yes | `store.GetTrace`; hex-encoded `model.TraceID` |
| `GET /v1/topology` | viewer | yes | `topology.Edges` |
| `GET /v1/incidents` | viewer | yes | `anomaly.Grouper.ActiveIncidents` |
| `GET /v1/investigations/{id}` | viewer | yes | `rca.Engine.Get` |
| `POST /v1/investigations/{id}/replay?mode=` | operator | yes | `mode` default `recorded`; unknown mode → 400 (FR-F12-12) |
| `POST /v1/investigations/{id}/steps/{stepID}/correct` | operator/approver | yes | |
| `GET /v1/remediation/actions` | viewer | yes | `remediate.Guard.List` |
| `POST /v1/actions` | operator | yes | propose only, `Idempotency-Key` required |
| `POST /v1/actions/{id}/approve` | approver | yes | `Idempotency-Key` required; Guard's own separation-of-duty enforced |
| `POST /v1/actions/{id}/rollback` | approver | yes | |
| `POST /v1/nl/query` | viewer | yes | `nl.Interpreter` + `nl.Answerer` (rules) |
| `POST /v1/mcp` | per-tool MinRole | yes | JSON-RPC 2.0; 3/12 tools dispatch, 9 documented stubs |
| `GET /v1/audit?verify=` | admin | **no** | deferred — no `auth.AuditSink` wiring in scope this wave |
| `POST /v1/identities`, `GET`, `DELETE` | admin | **no** | deferred — chat identity binding out of scope |
| `GET /v1/eval/runs*` | viewer/admin | **no** | deferred |
| `GET /v1/store/budget`, `/v1/sampler/*` | viewer | **no** | deferred |
| `GET /v1/tenants/{id}/erasure` | admin | **no** | deferred |
| `GET /grafana*` | viewer | **no** | deferred — Grafana JSON API datasource contract not started |
| `POST /v1/integrations/slack/commands`, `pagerduty/test` | HMAC/admin | **no** | deferred — webhook idempotency store not built |
| `GET /metrics` | operator | **no** | deferred |

Roughly half of DR-29 §29.1's row set is wired this wave; the rest (audit, identities, eval,
cost/retention, Grafana datasource, webhooks, metrics) is out of the ~40-minute time box and
flagged above rather than stubbed with fake data.

## MCP tool coverage (DR-29 §29.2, 12 tools)

Implemented (real store/topology data, end-to-end tested):

- `traceiq_search_traces` → `store.SearchSpans`
- `traceiq_get_trace` → `store.GetTrace`
- `traceiq_topology_neighbors` → `topology.Neighbors`

Stubbed (per-tool `MinRole` still enforced; calling one returns a JSON-RPC error
`{"code": -32002, "message": "tool ... is not implemented in this wave ..."}`, never a faked
success or a silently empty/wrong result):

`traceiq_query_red`, `traceiq_topology_edges`, `traceiq_list_incidents`,
`traceiq_get_investigation`, `traceiq_search_memory`, `traceiq_correlate_logs`,
`traceiq_correlate_metrics`, `traceiq_ask`, `traceiq_start_investigation`.

`tools/list` returns all 12 with an `implemented: bool` flag so a client can distinguish "callable"
from "documented."

`traceiq_propose_action` is correctly absent (FR-F12-6: withheld from MCP in v1).

RBAC ordering: a per-tool `MinRole` check runs BEFORE the not-implemented check, so an
unauthorized caller gets a 401-equivalent JSON-RPC error rather than learning whether the tool is
implemented (`TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented`).

## RBAC / auth seam

`internal/auth` ships interfaces only in this wave (no concrete `Authenticator`/`Authorizer`
exists anywhere in the tree). Following the same pattern `internal/nl` already established
(`nl.Authorizer` narrowing `auth.Authorizer`), this package holds `Deps.Auth`/`Deps.Authorizer` as
the real interfaces and fails closed when either is nil or denies: a missing/invalid caller is
401, a caller lacking the DR-25 capability is 403 — never a silent allow. Tests supply
`fakeAuthenticator`/`fakeAuthorizer` (in `internal/api/testutil_test.go`), the latter implementing
a genuine **capability matrix** (`roleCapabilities map[Role]map[Capability]bool`), not a role
hierarchy, matching `auth.go`'s own doc comment that admin does not structurally imply the others.

Capability constants added in `internal/api/middleware.go`: `CapTelemetryRead`,
`CapIncidentInvestigate` (reuses `nl.go`'s literal), `CapRemediatePropose`, `CapRemediateApprove`,
`CapAdmin`, `CapAuditRead`, `CapInvestigationReplay`, `CapInvestigationCorrect`. DR-25 §25.2's full
matrix was not read this wave (out of binding-read scope); these are a reasonable, documented
minimum rather than an invented full matrix.

`model.NewRealClock()` is itself `panic("not implemented")` upstream (a sibling wave's TODO). Test
wiring supplies its own minimal `wallClock` (real `time.Now`-backed, satisfies `model.Clock`),
scoped to `internal/api`'s tests only — production wiring (`cmd/traceiq`, out of scope) needs its
own real clock once `model.NewRealClock` lands.

## web/ — embedded SPA

`web/web.go` — `go:embed static`, `FS()` returns the SPA root. `internal/api/server.go` mounts it
at `/` with a History-API SPA fallback (unmatched paths serve `index.html`).

Screens shipped this wave (2 of F12 §4.4's 9, vanilla ES modules, no build step, no framework):

- **Overview** (`web/static/js/overview.js`) — RED tiles + active-incident table against the real
  `GET /v1/overview`.
- **Traces** (`web/static/js/traces.js`) — service/errors-only filter bar + results table against
  the real `GET /v1/traces`.

Deferred (not started): Topology, Investigations, Remediation, Chat, Eval, Cost & Retention,
Memory screens; dark/light theming (FR-F12-9); WCAG/axe-core keyboard-nav verification
(FR-F12-10); bundle-size budget check (< 500 KB gzipped, FR-F12's NFR — current bundle is small
but not measured in CI).

`model.*` DTOs (`Incident`, `Investigation`, `Action`, `TraceIndex`, …) carry no `json:` tags, so
REST responses serialize with Go's default (capitalized) field names — functional, but a later
pass should add tags for a cleaner wire contract; noted rather than silently worked around.

## RBAC test results

Covered in `internal/api/server_test.go`/`mcp_test.go`:

- Viewer-gated GET endpoints: allowed for `viewer`, 403 for a roleless caller
  (`TestOverview_ViewerAllowed`, `TestOverview_UnauthorizedCallerRejected`).
- `POST /v1/actions` (propose): 403 for viewer, 201 for operator, real `remediate.Guard` state
  machine runs (`TestActionsPropose_OperatorAllowed_ViewerDenied`).
- `POST /v1/actions/{id}/approve`: 403 for operator, 200 for approver
  (`TestActionsApprove_RBAC`) — exercises Guard's real separation-of-duty check too (different
  subject IDs for propose vs approve).
- `Idempotency-Key` required on propose (`TestActionsPropose_MissingIdempotencyKey`, 400
  `idempotency_key_required`, matching F12's stated error code).
- MCP per-tool `MinRole`: viewer rejected on an operator tool, operator passes RBAC then hits
  "not implemented" (`TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented`).

## Test count / build status

See the final chat message for the actual `go build`/`go vet`/`go test -v` run output and pass
count — this file is written before that final run completes; if numbers here and the chat
response disagree, the chat response is authoritative.

## Follow-up pass: anomaly.TopologyReader build-breaking interface mismatch

A later pass picked this wave back up to fix a build failure the prior pass's session limit cut
off before it could see:

```
internal\api\testutil_test.go:269:50: cannot use graph (variable of type *topology.LiveGraph) as
anomaly.TopologyReader value in argument to anomaly.NewGrouper: *topology.LiveGraph does not
implement anomaly.TopologyReader (missing method Has)
```

**Root cause.** `anomaly.TopologyReader` (`internal/anomaly/anomaly.go:47-50`, wave 12) and
`topology.Graph`/`topology.LiveGraph` (`internal/topology`, wave 10) were each written
independently, per DR-2's consumer-declared-interface rule — `anomaly` deliberately never imports
`topology`. Nobody had wired the two together until this wave's `internal/api` test harness tried
to pass a real `*topology.LiveGraph` where `anomaly.NewGrouper` wants an `anomaly.TopologyReader`,
and two real gaps surfaced:

1. **Shape mismatch on `Neighbors`.** `TopologyReader.Neighbors` is
   `(ctx, tid, service string, hops int, dir uint8) ([]string, error)`; `LiveGraph.Neighbors` is
   `(ctx, tid, service string, hops int, dir topology.Direction) (topology.Neighborhood, error)` —
   different `dir` type, different return shape (a flat neighbor list vs. a struct splitting
   upstream/downstream plus edges/hop-map).
2. **`Has` doesn't exist anywhere.** `TopologyReader.Has(ctx, tid, service) bool` has no
   counterpart on `topology.Graph` or `LiveGraph` at all — not a signature mismatch, a missing
   method. `internal/rca.TopologyReader` (`internal/rca/rca.go:199`) independently declares the
   same `Has`-only shape and is *also* never adapted to a concrete implementation anywhere in the
   tree yet (its own construction sites pass `nil` for the topology reader) — this gap isn't new to
   this pass, just newly visible now that something finally tried to wire a real `TopologyReader`.

**Fix chosen: adapter in `internal/api`, plus one small additive method on `LiveGraph`.**

The brief's option (a) — an adapter living in `internal/api`, which is the one package DR-2 lets
import every feature package — was preferred over widening `anomaly.TopologyReader` or
`topology.Graph`'s contracts (both are separately reviewed, wave 10 and wave 12). A pure adapter
turned out not to be sufficient on its own for `Has`: neither `Graph` nor `LiveGraph` exposes any
way to answer "has this service been seen" (edges only cover services that have made a call;
`ensureService` — `internal/topology/livegraph.go:173` — records every service touched by any
span, including edge-less INTERNAL-only ones, in a private `services map[TenantID]map[string]*ServiceNode`
that nothing outside the package could read). So the fix is the combination the brief flagged as
the fallback, applied narrowly:

- `internal/topology/livegraph.go`: added `func (g *LiveGraph) Has(ctx, tid, service) bool`
  (additive method, not part of the `Graph` interface, ~10 lines) — an `RLock`ed read of the
  existing `g.services[tid][service]` map that `ensureService` already maintains. No existing
  method signature changed; no other `Graph` implementer is affected.
- `internal/api/topology_adapter.go` (new file): `anomalyTopologyReader` wraps a narrow
  consumer-declared interface (`topologyNeighborHas`: `LiveGraph`'s real `Neighbors` signature
  + `Has`), satisfied structurally by `*topology.LiveGraph`. `Has` passes straight through.
  `Neighbors` converts `dir uint8 -> topology.Direction` by direct cast (the values are
  numerically identical: `Upstream=1/Downstream=2/Both=3` on both sides, confirmed against
  `internal/anomaly/grouper.go:141`'s only call site, `3 /* Both */`), then merges
  `Neighborhood.Upstream`+`Downstream` into one deduplicated `[]string` — matching how
  `internal/anomaly/grouper_test.go`'s `fakeTopo` stub and the grouper's only call site already
  treat "neighbors" as a flat set, not two sides.
- `internal/api/testutil_test.go:269`: now
  `anomaly.NewGrouper(anomaly.DefaultConfig(), newAnomalyTopologyReader(graph))`.

This was safer than editing `anomaly.TopologyReader` (would touch a reviewed package's published
contract and every existing implementer, including `grouper_test.go`'s `fakeTopo`) and safer than
widening `topology.Graph` (would force every `Graph` implementer, including any test doubles, to
grow a new method). The one `topology` package edit made is additive and scoped to the concrete
`LiveGraph` type only.

**Second bug found via the same fix.** Once the package compiled, `TestIncidents_RealGrouper`
failed (`want 1 incident, got 0`) — not a topology problem. `internal/api/testutil_test.go` had
been constructing the grouper with the zero-value `anomaly.Config{}` rather than
`anomaly.DefaultConfig()`. With `Grouping.MaxOpenIncidents == 0`,
`memGrouper.enforceMaxOpenIncidents` (`internal/anomaly/grouper.go:301-317`) sees `open(1) >
max(0)` on the very first `Add` and immediately expires the incident it just created, so
`ActiveIncidents` always came back empty. This was unreachable before (the package never compiled
far enough to run this test) and is a one-line fix: swap in `anomaly.DefaultConfig()`. Fixed in the
same edit as above.

**Confirmed out of scope, not touched.** `TestMCP_SearchTraces_RealStore` (`want 1 span from the
real store, got 0`) still fails. Traced to `internal/store/sqlite/hotindex.go`'s `SearchSpans` —
the `internal/api` call site (`mcp.go:236`, `store.SpanQuery{Service, Limit}`) is well-formed and
matches the interface; the zero result comes from inside the sqlite query itself. This is
`internal/store/sqlite`, explicitly flagged in the task brief as a sibling agent's concurrent scope
and off-limits here — left as-is and reported rather than fixed.

**Verification (this pass, GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0):**

- `go build ./...` — exit 0.
- `go vet ./...` (whole repo, not just `internal/api`) — exit 0.
- `go test ./internal/api/... -v` — 23/24 pass; the one failure is the
  `internal/store/sqlite` issue above (RBAC-gated endpoints, all 3 implemented MCP tools except
  `traceiq_search_traces`'s store lookup, and embedded `web/` asset serving all verified passing:
  `TestOverview_UnauthorizedCallerRejected`, `TestActionsPropose_OperatorAllowed_ViewerDenied`,
  `TestActionsApprove_RBAC`, `TestMCP_UnauthenticatedRejected`,
  `TestMCP_UnimplementedOperatorTool_RBACCheckedBeforeNotImplemented`,
  `TestMCP_GetTrace_RealStore`, `TestMCP_TopologyNeighbors_RealGraph`, `TestIncidents_RealGrouper`,
  `TestStaticRoot_ServesRealHTML`, `TestStaticSPARoute_FallsBackToIndex`, `TestStaticAsset_JS`, and
  13 more — see the chat response for the full list).
- `go test ./...` (full repo) — every package passes except `internal/api`'s single
  `TestMCP_SearchTraces_RealStore` above; `internal/store/sqlite`, `internal/store/tiered`,
  `internal/store/parquet` and `cmd/traceiq` all pass their own suites, so the "sibling agent's
  concurrent bug-fix work" the task brief warned might still be broken was, at the time of this
  run, not observably broken outside the one `internal/api` MCP test.

Files touched this pass: `internal/topology/livegraph.go` (additive `Has` method),
`internal/api/topology_adapter.go` (new), `internal/api/testutil_test.go` (wire the adapter +
`DefaultConfig()`). Nothing in `cmd/traceiq`, `internal/store`, or `anomaly`'s interface
definitions was touched.

## What's deferred (full list)

- REST: `/v1/audit`, `/v1/identities*`, `/v1/eval/runs*`, `/v1/store/budget`, `/v1/sampler/*`,
  `/v1/tenants/{id}/erasure`, `/grafana*`, Slack/PagerDuty webhooks, `/metrics`.
- MCP: 9 of 12 tools (documented stubs, per-tool RBAC still enforced).
- `api.AlertRouter` (P1/P2/P3 paging, FR-F12-8/14/15) — not started.
- Web: 7 of 9 screens, dark/light theming, WCAG/axe-core CI gate, bundle-size CI gate, WS/SSE
  live updates (Topology/Chat).
- `json:` tags on `model.*` response DTOs.
- Production `model.Clock` wiring (blocked on upstream `model.NewRealClock`).
