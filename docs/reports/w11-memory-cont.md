# W11 — internal/memory continuation

Continuation of an interrupted W11 pass. `internal/correlate` was already complete and untouched.
`internal/memory` had substantial implementation (fingerprint.go, memory.go, scorer.go, store.go) but
failed to build (`undefined: mathPow`) and had no tests. This pass fixed the build, read the full
implementation, wrote a comprehensive test suite, fixed one real bug found by TDD, and implemented the
previously-missing `RunbookImporter`.

## Build fix

`store.go:155` called an undefined local `mathPow(base, exp)`. The surrounding `pow()` helper computes
DR-19 §19.4's decay formula (`Weight * 0.5^(age/90d)`), which needs a *fractional* exponent — not
something a repeated-squaring loop (the kind of "local pow" the removed comment gestured at) can do
correctly. Added `"math"` to the import block and replaced the call with `math.Pow(base, exp)`.
`go build ./internal/memory/...` is clean.

## Bug found and fixed: lexicalScorer diluted fingerprint matches with free text

`internal/memory/scorer.go`'s `lexicalScorer.Score` originally pooled a candidate's `FingerprintTerms`
*and* its `Symptom`/`RootCause`/`BodyMarkdown` prose into one token bag, then Jaccarded that combined bag
against the query's `Fingerprint.Tokens` (`Query.Text` is empty on `Store.Similar`'s normal call path,
per FR-F08-2). Because a candidate's prose tokens have zero overlap with a fingerprint-only query, they
pad the denominator without ever appearing in the numerator — an **exact** fingerprint-token match against
a candidate carrying even minimal Symptom/RootCause text (e.g. `"s"`/`"r"`, one token each) computed
Jaccard = 1/3 = 0.33, under `min_similarity` (0.35). This meant `Store.Similar()` — FR-F08-2's core,
spec-mandated retrieval path, AC-F08-2 — returned **zero results for what should have been the store's
best match**, for any investigation record with nonempty prose (i.e. essentially all of them).

Caught by `TestStore_RecordAndSimilar_RoundTrip`, `TestStore_Record_LLMReasoner_ProvenanceAndTrustTier`,
and `TestStore_Correct_SupersederRankedAboveOriginal_WeightLowered`, all of which failed with "0 results"
before the fix (`TestStore_Correct_WeightFloorsAtZero` then panicked on an empty slice index, a knock-on
of the same bug).

**Fix:** `lexicalScorer.Score` now scores fingerprint-token Jaccard and free-text Jaccard as two separate
components. Fingerprint-token Jaccard alone is the score when `Query.Text` is empty (the normal
`Similar()` case). When free text is present, the blend is `0.7*fpScore + 0.3*textScore` — a judgment
call (see below) reflecting DR-19 §19.2's framing of fingerprint tokens as the primary similarity signal
and free text as `Search()`/FTS's job, not `Similar()`'s.

## Judgment calls (spec ambiguity)

1. **`textWeight = 0.3` blend factor** (`scorer.go`) — DR-19 §19.3/§19.2 specify Jaccard scoring but not
   how a fingerprint component and a free-text component combine when both are present. No test in this
   pass exercises non-empty `Query.Text` against records with differing prose (out of the "at minimum"
   list), so this weight is not load-bearing for any AC yet — flagged for a follow-up pass if/when
   `Query.Text` gets a real caller.
2. **`RunbookImporter` implementation** (`internal/memory/runbook.go`, new file) — FR-F08-5 requires it
   and it was absent (interface-only) before this pass. Implemented as `inMemoryRunbookImporter`,
   constructed via `NewRunbookImporter(s Store)`, which type-asserts down to the concrete
   `*inMemoryStore` built by `NewInMemoryStore` (DR-19 §19.3's `Store` interface is bound verbatim and
   has no "insert a raw `Kind=Runbook` record" method, so this was the least-invasive way to reach the
   tenant maps without adding one). A minimal front-matter parser handles `key: value` scalars and
   `key: [a, b, c]` flow-sequence lists — sufficient for `title`/`service`/`symptom_tags` — rather than
   pulling in a full YAML dependency (not on `memory`'s allowed-import list: model, config, tenant,
   store, llm).
3. **Runbook `symptom_tags` map to the `errsig:` token class** — F08 §4.4's pseudocode doesn't spell out
   which closed token class `symptom_tags` becomes; mapping it to `errsig:` (rather than inventing a new
   class) is what makes `Similar()` interleave runbooks with investigations that share the same error
   signature, satisfying FR-F08-5's "indexing them into the same fingerprint-similarity space."
4. **No secret scrubbing on runbook import** — §4.4's pseudocode calls `secretScrubber.Scrub(body)`, but
   no scrubber package is in `memory`'s allowed-import list and F06/F07's scrubber lives outside it.
   Documented as a deviation, not silently dropped, matching the posture `store.go`'s existing type doc
   already takes for the SQLite-vs-in-memory and MinHash-vs-pairwise deviations.
5. **`Correct`/`Confirm` tenant-scoping test expectation** — DR-19 §19.4 doesn't explicitly say what
   happens when `tid` doesn't own `TargetID`/`id`; verified (already correctly implemented, not a bug)
   that both return an error rather than silently succeeding or mutating another tenant's record — this
   is exercised in the tenant-isolation section below since a misbehavior here would be an
   authorization bypass, not just a functional gap.

## Test coverage vs AC-F08-*

| AC | Covered by | Result |
|---|---|---|
| AC-F08-1 (fingerprint stability, tenant distinctness, Provenance/TrustTier) | `TestComputeFingerprint_DeterministicUnderReordering`, `TestComputeFingerprint_TenantScoped_NeverCollidesAcrossTenants`, `TestComputeFingerprint_CappedAtMaxTokens`, `TestStore_RecordAndSimilar_RoundTrip`, `TestStore_Record_LLMReasoner_ProvenanceAndTrustTier`, `TestStore_Record_RequiresTenant` | Pass |
| AC-F08-2 (bounded 3-step `Similar()`, correct ranking, scorer swap) | `TestLexicalScorer_RanksCloserMatchHigher`, `TestStore_RecordAndSimilar_RoundTrip`, `TestSimilar_BoundedByMaxCandidates` | Pass (scorer bug fixed — see above) |
| AC-F08-3 (Correction ranks above superseded; superseded weight strictly lower) | `TestStore_Correct_SupersederRankedAboveOriginal_WeightLowered`, `TestStore_Correct_WeightFloorsAtZero`, `TestStore_Confirm_BoostsWeight_CapAtTwo`, `TestEffectiveWeight_DecaysButNeverPersists` | Pass |
| AC-F08-4 (runbooks tagged `Kind: Runbook`, excluded from pruning) | `TestRunbookImport_ValidFrontMatter_ParsesAndIndexes`, `TestRunbookImport_MissingRequiredFields_Rejected`, `TestRunbookImport_ExcludedFromPruning`, `TestConsolidate_DoesNotMergeAcrossKinds` | Pass |
| AC-F08-5 (byte-reproducible JSON/Markdown export, `effectiveWeight` excluded, RFC3339 UTC, LF) | `TestExport_JSON_ValidAndReproducible`, `TestExport_Markdown_Valid`, `TestImport_UnknownSchema_Rejected`, `TestImport_IDCollision_NeverOverwrites` | Pass |
| AC-F08-6 (consolidation merges fingerprint family, prunes stale non-pinned) | `TestConsolidate_MergesNearDuplicateFingerprintFamily`, `TestConsolidate_PrunesStaleNonPinnedRecords` | Pass |
| AC-F08-7 (p99 < 200 ms **and** ≤ 500 candidates at 100k rows) | **Not covered** — this pass's `inMemoryStore` is a documented O(n) full-tenant-scan deviation from the SQLite + `memory_fp_token` inverted index the spec requires (see `store.go`'s type doc, pre-existing from the prior session); a 100k-row load test would exercise the in-memory map's raw scan performance, not the bounded-index architecture AC-F08-7 is actually gating. `TestSimilar_BoundedByMaxCandidates` covers the *topK*-bounding half only, seeded at `maxCandidates+50` records, as a partial proxy. |

## Tenant isolation (security-relevant) — verified carefully

Five dedicated tests, all passing:

- `TestTenantIsolation_SimilarNeverReturnsOtherTenantsRecords` — tenant B, querying with an identical
  service/error-signature shape to tenant A's stored investigation, gets zero hits. Also verified that
  querying tenant B **with tenant A's own `Fingerprint` object** (a forged/replayed fingerprint, since
  `Fingerprint.TenantID` is caller-settable data, not enforced by the type system) still returns nothing,
  because `Similar()`'s candidate generation scopes strictly by the `tid` *argument*, not by whatever
  tenant happens to be embedded in the `fp` argument — confirms isolation is structural (candidate scan
  is per-tenant map), not merely "the hash happens to differ."
- `TestTenantIsolation_GetNeverCrossesTenants` — tenant B `Get()`-ing tenant A's record ID by value
  returns an error, not tenant A's data.
- `TestTenantIsolation_SearchNeverCrossesTenants` — free-text `Search()` is also tenant-scoped (not just
  the fingerprint path).
- `TestTenantIsolation_CorrectAndConfirmCannotTargetOtherTenant` — tenant B cannot `Correct()` or
  `Confirm()` a record ID belonging to tenant A (both return errors); tenant A's record is asserted
  unchanged afterward (weight, `Confirmations`, `Corrections` all untouched) — rules out a partial-mutation
  bug where the error is returned but a side effect still landed.
- `TestTenantIsolation_ExportOnlyIncludesOwnTenant` — `Export()` for tenant A contains no trace of tenant
  B's symptom text and reports `count: 1`.

No isolation bug was found — `tenantMap(tid)` scoping in every `Store` method plus the tenant-inside-the-
hash-preimage construction in `hashTokens` (`fingerprint.go`) hold up under all five angles tried.

## Commands run (all green for `internal/memory`)

```
go build ./internal/memory/...   # clean
go vet ./...                     # clean, no findings anywhere
go test ./internal/memory/... -v # 26/26 pass
go build ./...                   # clean
go test ./...                    # all packages pass except internal/archtest
```

`internal/archtest`'s `TestPackageAdjacency` fails: `internal/rca/rules/rules.go` has no DR-2 adjacency
table entry. This is unrelated to `internal/memory` (it's about `rca/rules`, a package this pass never
touched) and out of this pass's scope lock (`internal/memory/**` only) — left as-is, flagged here rather
than silently ignored.

## Remaining / follow-up

- `Store` is in-memory, not SQLite-backed with a `memory_fp_token` inverted index (pre-existing deviation
  from the prior session, documented in `store.go`'s type doc) — this is why AC-F08-7 (p99 latency + ≤500
  candidates at 100k rows, as a function of the *bounded-index* architecture) isn't meaningfully testable
  yet; a load test against the current O(n)-scan implementation would validate the wrong thing.
- `Consolidate()` uses direct pairwise Jaccard (documented O(n²) deviation), not FR-F08-7's banded MinHash
  LSH — the ≤2%-of-pairs-examined bound from AC-F08-6 is untested because the current implementation
  doesn't have that property to test.
- `rca.Sanitizer.Wrap` on `ProvLLMAuthored`/`ProvImported` retrieval (FR-F08-9) is not implemented in
  `internal/memory` — correctly so, since `memory` must not import `rca` (DR-2) and the wrap happens on
  the `rca` side per the sequence diagram; not this package's responsibility, not tested here.
- `textWeight` blend factor (judgment call #1 above) has no test exercising non-empty `Query.Text` — flag
  for whoever wires a `Search()`-with-fingerprint caller.
- `internal/archtest`'s `rca/rules` adjacency gap (see above) is pre-existing and out of scope; flagging
  for a separate pass.
