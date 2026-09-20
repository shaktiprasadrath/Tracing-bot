# W1 scaffold report — compiling skeleton continuation

Continuation of an interrupted scaffold run. `go build ./...`,
`go vet ./...` and `go test ./...` (which runs `internal/archtest`) all
pass at the end of this session. `gofmt -l .` is clean.

## Packages done this session

- **`internal/k8s`** — added `k8s.go` (doc.go pre-existed). Verbatim from
  DR-24 (`docs/architecture/06-decision-register.md:2537-2594`): `Verb`,
  `Request`, `Result`, `Executor`, `BuildArgv`, `MinimalEnv`.
- **`internal/auth`** — new package, `doc.go` + `auth.go`. Verbatim from
  DR-25 §25.1 (line 2595-2740): `Role`, `SubjectKind`, `ChatIdentity`,
  `Subject`/`Principal`, `Authenticator`, `Authorizer`, `LimitClass`,
  `Key`, `RateLimiter`, `AuditSink`/`AuditLog`, `SecretSource`,
  `EgressDialer`, `IdentityBinding`, `IdentityStore`. Several supporting
  types the register names only in a method signature (never prints a
  dedicated block for) were reconstructed minimally and flagged inline
  with `// See docs/reports/w1-scaffold.md`: `ChatRequest`, `Capability`,
  `ResourceRef`, `Decision`, `Event`, `Receipt`, `Filter`, `VerifyReport`,
  `Anchor`.
- **`doc.go` added** (purpose, feature IDs, DR-2 allowed imports) for
  every previously-empty package: `internal/topology` (DR-13),
  `internal/anomaly` (DR-14), `internal/correlate` (DR-20),
  `internal/memory` (DR-19), `internal/remediate` (DR-22/23),
  `internal/sampler` (DR-10/11/12), `internal/rca` (DR-15-18),
  `internal/nl` (DR-35), `internal/eval` (DR-36), `internal/ingest`
  (DR-28), `internal/api` (DR-26/29), `internal/store` (DR-6-9).
  These packages still have **no interfaces populated** — see Remaining.
- **`internal/archtest/archtest_test.go`** — new. Parses every non-test
  `.go` file under `internal/` and `cmd/` with `go/parser`
  (`parser.ImportsOnly`, stdlib only, no new deps), checks each import
  against a Go-literal copy of DR-2's adjacency table
  (`docs/architecture/06-decision-register.md:119-185`), fails on any
  edge not listed, and separately fails if `internal/model` imports
  anything non-stdlib. Verified it actually catches a violation (injected
  and reverted a `k8s -> config` import during this session — the test
  failed with the expected message, then passed again after revert).
- **`cmd/traceiq/main.go`** — new. `--version` and `--config` flags;
  `--config` wires to the pre-existing `config.Load` stub (returns
  `config.ErrNotImplemented`), so the binary compiles, runs `--version`,
  and exits non-zero with a clear message on any other invocation rather
  than silently doing nothing.
- **`Makefile`**, **`scripts/ci.sh`**, **`scripts/ci.ps1`** — new. Both
  scripts run gofmt check, vet, build, test, a conditional `-race` pass
  (attempted with `CGO_ENABLED=1` only if a C compiler is found on
  `PATH`, otherwise skipped with a warning — the base env everywhere else
  is `GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 GOFLAGS=-mod=readonly` per the
  task's pinned toolchain), `govulncheck` and `staticcheck` via
  `go run <module>@latest` (no new go.mod entries). Ran the gofmt/vet/
  build/test steps directly this session (not the network-fetching
  govulncheck/staticcheck steps, to stay in budget); syntax-checked
  `ci.sh` with `bash -n`.
- **`README.md`** — new. Two-paragraph overview, repo layout, build/test
  instructions, docs pointer.
- Fixed one pre-existing `gofmt` violation in `internal/llm/llm.go`
  (whitespace-only, from before this session) so `gofmt -l .` is clean.

## Interfaces per package (origin DR)

| Package | Types/interfaces | DR |
|---|---|---|
| `model` | `TenantID`, `KeepReason`, `Window`, + prior session's types | DR-4 |
| `config` | `Config` (nested per-DR blocks) | DR-0 §7 umbrella, amended by many DRs |
| `tenant` | `ChatBinding`, `Policy`, `Resolver`, `PolicyStore`, `WithTenant`/`FromContext` | DR-3 |
| `selfobs` | `Recorder`, `ComponentHealth`, `ReadinessCheck`, `Registry` | reconstructed (DR-28/DR-33 usages) |
| `bus` | `Message`, `Bus` | DR-32 §32.3 |
| `cluster` | `Elector` | DR-27 §27.2 usage |
| `llm` | `StopReason`, `Message`, `ToolSchema`, `Request`, `Usage`, `Response`, `Client`, `NewClient` | DR-34 §34.1 |
| `k8s` | `Verb`, `Request`, `Result`, `Executor`, `BuildArgv`, `MinimalEnv` | DR-24 |
| `auth` | see above | DR-25 §25.1 |

`topology`, `anomaly`, `correlate`, `memory`, `remediate`, `sampler`,
`rca`, `nl`, `eval`, `ingest`, `api`, `store` have `doc.go` only — no
interfaces yet.

## Remaining — `PARTIAL`

Explicitly out of this session's time budget, in priority order for a
follow-up pass:

1. **`internal/store`** (+ `sqlite`/`parquet`/`tiered` driver
   subpackages) — DR-6 (hot index), DR-7 (cold store), DR-8 (sharding),
   DR-9 (assembly). Largest remaining block (line 385-956 combined).
2. **`internal/sampler`** — DR-10 (line 840-956), DR-11 (957-1037),
   DR-12 (1038-1114).
3. **`internal/topology`** — DR-13 (1115-1209), including the full
   `Graph` interface the DR-2 header promises.
4. **`internal/anomaly`** — DR-14 (1210-1506, the longest single DR).
5. **`internal/rca`** — DR-15 (1507-1630), DR-16 tool set (1631-1746),
   DR-17 budget (1747-1834), DR-18 replay (1835-1927).
6. **`internal/memory`** — DR-19 (1928-2066).
7. **`internal/correlate`** — DR-20 (2067-2169).
8. **`internal/remediate`** — DR-22 (2253-2368), DR-23 (2369-2536).
9. **`internal/ingest`** — DR-28 (2892-2940).
10. **`internal/api`** — DR-26 (2741-2822, listeners/TLS/limits), DR-29
    (2941-3000, endpoint table/MCP tools/idempotency).
11. **`internal/nl`** — DR-35 (3283-3391).
12. **`internal/eval`** — DR-36 (3392-3547).
13. `store/sqlite`, `store/parquet`, `store/tiered` need real driver
    types once `store`'s own interfaces exist; `store/blob` and
    `store/clickhouse` directories don't exist yet and DR-2's row
    mentions them — create when their DRs (not yet read this session)
    are implemented.
14. `cmd/traceiq/main.go` wires only `config.Load` + flags; the rest of
    the composition root (cluster election, bus, selfobs registry, auth,
    every feature constructor, the api server) is not implemented — by
    design for this types-only scaffold, but flagged so it isn't mistaken
    for done.
15. `scripts/ci.sh` / `scripts/ci.ps1`'s `govulncheck`/`staticcheck` steps
    were not executed this session (network fetch of the tool modules);
    they're expected to work (standard `go run module@latest` pattern)
    but unverified.

## Register ambiguities found at the code level

- **DR-25's auth types the register only names in a method signature.**
  `Authorizer.Can`'s `Capability`/`ResourceRef`, `RateLimiter.Allow`'s
  `Decision`, and `AuditSink`'s `Event`/`Receipt`/`Filter`/
  `VerifyReport`/`Anchor` are referenced by the verbatim §25.1 block but
  never given their own `type ... struct` in the register text. Each was
  reconstructed minimally in `internal/auth/auth.go` with an inline
  comment pointing back here rather than guessed silently. A future pass
  should cross-check these against DR-26/DR-27/DR-29's prose (audit row
  shape, rate-limit response codes) before treating them as final.
- **`auth.ChatRequest`** (the `AuthenticateChat` parameter) is likewise
  undeclared in the register; reconstructed from DR-25 §25.4's prose
  ("(platform, workspace_id, platform_user_id) resolves ... to a
  provisioned Subject", channel-scoped approvals, HMAC signature).
- **`store`/`store/sqlite`/`store/parquet`/`store/tiered`/`store/blob`/
  `store/clickhouse` share one DR-2 adjacency row** ("model, config,
  tenant, topology") but the driver subpackages plainly need to construct
  and return `store`'s own concrete types (e.g. a hypothetical
  `store.Trace`) to implement `store`'s interfaces — which is unreachable
  without importing `store` itself. `internal/archtest`'s adjacency table
  resolves this by additionally allowing each `store/*` subpackage to
  import `store`; this is called out both in the test's own doc comment
  and here rather than silently assumed. This should be confirmed against
  DR-6/DR-7's full text (not fully read this session past the line-range
  index) when `store` itself is implemented.
- **`selfobs.Recorder`/`ComponentHealth`/`ReadinessCheck`/`Registry`**
  (pre-existing, from before this session) are noted in that package's
  own doc comment as "a reasonable minimal seam reconstructed from
  [scattered metric-name] usages" rather than a printed DR block — same
  pattern as the auth reconstructions above, carried over here for
  visibility since this report is now the canonical ambiguity log
  (superseding the file the old comment pointed at,
  `docs/reports/scaffold-report.md`, which does not exist in this repo).
- **`tenant.Policy.MaxReplicas`** (pre-existing) has the same
  reconstructed-from-a-cross-reference status, per its own doc comment.

## Build/test status

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...   # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...     # PASS
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./...    # PASS (archtest; all other packages have no test files yet)
gofmt -l .                                          # clean
```
