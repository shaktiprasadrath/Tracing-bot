# w16 — Review: cmd/traceiq + internal/config

Binding sources read: `docs/architecture/01-system-architecture.md` §7 (config model, L2435-2654),
`docs/architecture/features/X-OPS-deployment.md` (full — startup/shutdown order, readiness,
backup). Prior work reviewed: `docs/reports/w15-cmd-wiring.md` (the two bugs the smoke test found
and fixed), `docs/reports/w15-api-web.md` / `docs/reports/w16-review-api-web.md` (internal/api's
own wave and review).

## Verdict: **APPROVE-WITH-FIXES**

Three Major findings, all **fixed** in this pass, plus three new regression tests added (cmd/traceiq
had exactly one test before this wave). No Blockers. Both w15 bug fixes are re-verified correct —
one exactly as fixed, one needed a deeper fix than the original test happened to exercise. `api`
wiring is confirmed still absent and confirmed correctly *not* a quick fix; left as an accurate,
updated TODO rather than rushed. Full `go build ./... && go vet ./... && go test ./... -count=1` is
green.

## Findings table

| # | Severity | Area | Finding | Status |
|---|---|---|---|---|
| 1 | Major | `cmd/traceiq/system.go` | `runStoreBridge`/`runBaselineFeed` raced `ctx.Done()` against a pending `decisions`/`red` channel read; at shutdown, Go's random `select` case choice could silently drop an already-decided `Keep=true` trace (or a RED sample) instead of persisting it. | **FIXED** |
| 2 | Major | `cmd/traceiq/system.go` | `sampler.Manager.DrainRED` was never called anywhere in `cmd/traceiq` (only `Manager.Run`, which this package doesn't use, calls it alongside `Tick`) — `runBaselineFeed`'s `REDSamples()` channel was permanently starved, so the anomaly baseline never received an observation in the real binary regardless of shutdown timing. | **FIXED** |
| 3 | Major | `cmd/traceiq/system.go` | Even after closing (1), a decision dequeued by the *normal* (non-drain) loop path an instant before `Shutdown` cancels `bridgeCtx` could still have its `tiered.Append`/`baseline.Observe` call aborted mid-flight with `context canceled`, because the write used the loop's own cancellable `ctx`. Found while testing the fix for (1). | **FIXED** |
| 4 | Major (out of scope, flagged not fixed) | `internal/ingest/queue.go` | `sinkQueue.drain` has the identical `select{ case <-ch: ...; case <-ctx.Done(): return }` race as (1), and `Server.Stop` cancels the drain context without waiting for it to drain — a span batch already enqueued but not yet consumed at `Stop()` time can be silently dropped. Same bug class, different package (outside this wave's `cmd/traceiq`/`internal/config` scope). | Flagged via `spawn_task` (task_87d08322), not fixed |
| 5 | Minor | `cmd/traceiq/system.go` | `Start`'s TODO comment describing why `internal/api` isn't wired was stale (written when `internal/api` was still mid-edit by a sibling wave; it has since stabilized and its own tests are green). | **FIXED** (comment rewritten with the current, concrete blocker) |
| 6 | Minor | `internal/config/validate.go` | `Validate` only implements DR-26 §26.3 rules 2/3/4/8/9 of the ten-rule set; rules 1 (per-listener TLS+auth), 5 (correlate tenant_mode), 6 (byte-budget/tenancy), 7 (derived kept-span rate), 10 (anomaly max_keys) are unimplemented. This is honestly self-documented in the file's own doc comment, not hidden, and scoped to what this wave's dev-mode wiring depends on — but a config that violates rule 1 (e.g. `auth.mode: token` + `tls.enabled: false` on a routable listener) would currently start without error. | Not fixed — pre-existing, explicitly documented gap; flagged here for visibility |
| 7 | Minor (test coverage) | `internal/store/sqlite` | No existing test exercises a `path_signature` value at exactly the `math.MinInt64` bit pattern (2^63) end-to-end through `GetTrace`/`SearchTraces` — the round-trip is proven correct analytically (below) but isn't regression-tested at that boundary. | Not fixed — out of scope package, flagged only |

## 1. Startup/shutdown order vs X-OPS §4.6

`newSystem`/`Start` follow the documented order closely: config → store (hot then cold, with
`ColdStore.ReplayWAL` before receivers bind, per X-OPS's "startup reconciliation is the mirror
image" note) → topology → sampler → ingest → anomaly (baseline construction) → rca. `Shutdown`
follows X1–X12 for the subset of components this dev-mode wiring actually owns: `/readyz` fails
first (X1), ingest stops (X2/X3), sampler `FlushAll` (X4), topology flush (X5), baseline checkpoint
(X6), cold seal+bind (X9), hot close (X10). X7/X8/X11 are correctly no-ops (rca/remediate have no
live event loop in this wave's wiring; audit is `internal/api`'s concern, not yet wired) — this is
the same documented scope reduction as w15, not a new gap.

**One real ordering fix this wave** (see finding 1/2/3 detail below): the finalize ticker previously
shared `bridgeCancel`/`bg` with the store/baseline bridges and was told to stop only *after*
`FlushAll`, with no join — leaving a window where a `Tick`-driven `finalize()` could still be
writing to the same channels concurrently with, or after, `FlushAll`'s own sweep. `Shutdown` now
stops and joins the ticker **first**, then calls `FlushAll`, then does a final `DrainRED`, and only
then cancels the store/baseline bridges — matching X4's intent ("force-complete every open trace...
then truncate the sampler WAL segments") with an actual happens-before guarantee instead of a
best-effort ordering.

## 2. Re-verification of the two w15 bug fixes

### Bug 1 — `path_signature` int64/uint64 round-trip: **verified correct, no remaining edge case**

Go's `int64`↔`uint64` conversion is a pure bit-pattern reinterpretation for same-width integer
types (not implementation-defined, not saturating) — this was checked directly rather than assumed:

```
uint64=0                    -> int64=0                    -> uint64=0                    ok
uint64=1                    -> int64=1                    -> uint64=1                    ok
uint64=MaxUint64             -> int64=-1                   -> uint64=MaxUint64             ok
uint64=MaxInt64               -> int64=MaxInt64               -> uint64=MaxInt64               ok
uint64=2^63 (MinInt64 pattern) -> int64=MinInt64 -> uint64=2^63              ok
uint64=MaxUint64-1           -> int64=-2                   -> uint64=MaxUint64-1           ok
```

The write path (`internal/store/sqlite/hotindex.go`, `WriteBatch`: `int64(t.PathSignature)`) and
read path (`GetTrace`/`SearchTraces`: scan to local `int64`, then `uint64(pathSig)`) are exact
inverses for every one of the 2^64 possible hash values, including the `math.MinInt64` bit pattern
named in the review brief. There is no remaining edge case in the fix itself. The one gap found is
test coverage, not correctness: no existing test drives a `path_signature` at exactly that boundary
through the store round-trip (finding 7, flagged, not fixed — out of scope package for this wave).

### Bug 2 — double-seal on shutdown: **verified idempotent across all three block states, not just the one the test hit**

Traced `Shutdown`'s X9 loop (`s.cold.DueBlocks(...)` then per-block `Seal`+`BindColdBlock`) against
`store/parquet`'s actual state machine (`parquet.go`):

- **Never-sealed (open)**: `DueBlocks` lists it (Shutdown forces `now+24h` so every open block is
  "due" regardless of its real age); `Seal` moves it `open → sealed` and writes the manifest. Bound
  correctly.
- **Already-sealed**: `Seal` deletes the block from `s.open` under lock before releasing the lock
  (`parquet.go:342`), so a sealed block is structurally absent from `s.open` and can never be
  returned by a second `DueBlocks` call within the same `Shutdown` invocation — not just "the test
  didn't hit this case", but unreachable by construction.
- **Sealed-and-bound**: same as above (sealed blocks never reappear in `s.open`); a `BindColdBlock`
  failure is logged and the loop continues without re-attempting `Seal` on that block, so there's no
  retry-into-double-seal path either.

The `blockID`-doubles-as-`wal_segment` convention the fix relies on (`s.hot.BindColdBlock(ctx,
manifest.Tenant, db.BlockID, manifest)`) is a real, documented invariant
(`internal/store/store.go:701-704`, matching `store.TieredStore.SealAndBind`'s identical call
shape) — not a coincidental parameter match. **Verdict: genuinely fixed for all three states, not
merely the one failure mode the smoke test happened to trigger.**

## 3. `internal/api` wiring — confirmed still absent; confirmed correctly *not* a quick fix

Re-checked the premise directly: `go test ./internal/api/...` is green (the `TestMCP_SearchTraces_RealStore`
failure w15 called out as pre-existing/out-of-scope is gone — the sibling wave-16 api agent's own
review, `docs/reports/w16-review-api-web.md`, confirms this). So `internal/api` is in fact stable now.

Whether it's a *quick* wiring job was checked by reading `api.Deps` (`internal/api/api.go`) against
what `System` actually constructs. `Deps` has ~20 fields: `Auth`, `Authorizer`, `RateLimit`,
`Audit`, `Identities`, `Tenants`, `Policies`, `Topology`, `Remediate`, `Interpreter`, `Answerer`,
`Eval`, `Investigations`, `Memory`, `Correlate`, `Anomaly`, `Baselines`, `Deploys`, `Sampler`,
`Store`, `Cold`, `K8s`. `System` today constructs concrete values for only `Topology`, `Sampler`,
`Store`/`Cold` (via `tiered`), and `Investigations` (the rules-only `rcaEng`) — a handful of the ~20.

The decisive blocker: **`internal/auth` ships only interfaces, with no concrete implementation
anywhere in the repo** (confirmed by `grep` across `internal/auth/*.go` — only `auth.go`/`doc.go`
exist, both type/interface declarations, no `func New...Authenticator` or similar; `middleware.go`'s
own doc comment says as much: "no concrete Authenticator/Authorizer implementation exists yet").
`middleware.go` fails closed on a nil `Auth`/`Authorizer` (401/403, never a pass-through) — correct
and safe, but it means wiring `api.HTTPServer` today with only the fields `System` already has would
produce a process that serves `/healthz`, `/readyz`, and the static SPA shell, then **401s on every
REST and MCP call**. That's not "the binary can now serve the API" — it's a process that looks
configured while being non-functional, arguably worse than the current state where the gap is at
least visible (no api.endpoint at all).

**Decision: left unwired**, matching the review brief's own fork ("if larger than a quick wiring
job, leave it as a clearly documented TODO instead of rushing it"). The `Start`'s TODO comment in
`system.go` was stale (written when `internal/api` was still mid-edit) and has been rewritten to
name the actual current blocker (no concrete `auth.Authenticator`/`auth.Authorizer`) and the ~20-field
`Deps` surface, so the next wave doesn't have to re-derive this.

## 4. Graceful shutdown under active work

**Before this wave: no test existed for this at all.** `cmd/traceiq` had exactly one test
(`TestSystem_EndToEnd_OTLPSpanReachesStore`), and it only calls `Shutdown` *after* polling until the
trace is already confirmed readable — the happy path, not shutdown racing live work. Investigating
this gap is what surfaced findings 1–3 above.

Three tests added (`cmd/traceiq/shutdown_test.go`):

- `TestSystem_DrainStoreBridge_DrainsBufferedDecisionsWithoutLoss` — deterministic, white-box: feeds
  `drainStoreBridge` exactly the situation `Shutdown`'s X4 step guarantees (channel closed, producer
  genuinely stopped) and asserts every buffered `Keep=true` decision reaches the store. Doesn't
  depend on winning a timing race, so it fails reliably on a regression.
- `TestSystem_RunSamplerTicker_FeedsBaselineViaDrainRED` — starts the real system, ingests one
  error span, polls `baseline.Stats().Keys` — would hang/fail forever before the fix (finding 2).
- `TestSystem_Shutdown_MidIngest_NoHangOrGrossLoss` — drives 200 traces into the sampler directly
  (bypassing `internal/ingest`'s own queue, see finding 4), lets the real ticker/bridge run
  concurrently for a bounded window (measured: store bridge drains ~13 decisions per 20ms against
  real sqlite+parquet disk I/O, so a large genuinely in-flight backlog exists at 60ms), then calls
  `Shutdown` concurrently and asserts it returns within a bound (catches hangs) and that most
  already-decided traces survive (catches gross data loss; a small tail loss is tolerated by design
  since traces still genuinely *open* at `FlushAll` time are legitimately shed as `KeepShed` per
  X-OPS X4 — that's correct behavior, not a bug). Verification against `sys.tiered` after `Shutdown`
  would be meaningless (`Shutdown` already closed `sys.hot`/`sys.cold`), so this test reopens fresh
  store handles against the same on-disk files to check what was actually durably persisted.

Ran the mid-ingest test 13 times total (5 + 8) with no failures after the fix; before the fix it
failed close to 100% of the time in this environment (the backlog at shutdown was consistently large
enough — tens to hundreds of items — to make the race land almost every run, not a rare flake).

## 5. Config validation — fails loudly, with an honestly-scoped subset of rules

`Load` (`internal/config/load.go`): malformed YAML → `yaml.Unmarshal` error → `Load` returns an
error (fail-fast, never silently ignored). An unknown key → `decodeStruct` error → `Load` returns an
error (`TestLoad_UnknownKeyIsStartupError`, passing) — matches 01 §7's "unknown keys are a startup
error, not a warning." Out-of-range/invalid enum values on `server.mode`/`server.profile` are
rejected by `Validate`. A **missing** config file is correctly *not* an error (FR-XOPS-1's zero-file
dev start).

`Validate` (`internal/config/validate.go`) implements DR-26 §26.3 rules 2, 3, 4, 8, 9 — each tested.
Its own doc comment explicitly lists rules 1/5/6/7/10 as **not implemented** in this pass. This is
honest self-disclosure, not a hidden gap, and scoped to what this wave's dev-mode wiring actually
depends on holding — but it means, concretely, that a config with `auth.mode: token` on a
non-loopback listener with `tls.enabled: false` (rule 1 — the exact "bearer tokens in cleartext"
case §7 calls out as the reason for that rule) would start today without error. Flagged as finding 6
(Minor, not fixed — real but pre-existing and out of this wave's two-package scope, and already
disclosed in-code).

## 6. Other correctness/leak/test-quality notes

- No resource leaks found in `newSystem`'s construction-failure paths (hot/cold are closed on every
  later failure) or in `Start`'s new ticker goroutine (the failure path added alongside the fix now
  calls `tickerCancel()` before `cancel()` if `ing.Start` fails, so the ticker goroutine doesn't
  outlive a failed `Start`).
- Searched `cmd/traceiq` for every other `ctx.Done()` site to confirm the fix's scope was complete;
  the only other one (`clock.go`'s `realClock.Sleep`) is a plain cancellable sleep with no buffered
  channel to drain, not the same bug class.
- Test quality was the biggest pre-existing gap in this package (one test, happy-path only); partially
  closed by the three tests above, though `internal/ingest`'s analogous gap (finding 4) remains.

## Verification

```
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go build ./...                    # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go vet ./...                      # clean
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./... -count=1            # all packages ok, including internal/api
GOTOOLCHAIN=go1.27.1 CGO_ENABLED=0 go test ./cmd/traceiq/... -run TestSystem_Shutdown_MidIngest -count=8   # 8/8 pass
```

## Files changed

- `cmd/traceiq/system.go` — ticker given its own cancel/join lifecycle, stopped before `FlushAll`;
  `runSamplerTicker` now calls `DrainRED` every tick; `runStoreBridge`/`runBaselineFeed` drain
  remaining buffered items after cancellation instead of racing `ctx.Done()` against a pending read;
  all store/baseline writes use a `drainContext()` that outlives the loop's own cancellable `ctx`;
  `Start`'s stale `internal/api` TODO rewritten with the current concrete blocker.
- `cmd/traceiq/shutdown_test.go` (new) — three regression tests described in §4.
