# Worker report — F09, F10, X-SEC, X-OPS

Scope: apply every DR from `docs/architecture/06-decision-register.md` whose "Docs to change" row
names `X-SEC` or `X-OPS`; spot-check F09/F10 headers. Edits limited to `X-SEC-security.md`,
`X-OPS-deployment.md`, `F09-guarded-remediation.md`, `F10-natural-language.md` (read-only spot
check), and this report. Register and all other docs were not touched.

## Method

Ran `grep -n "Docs to change"` over the register, cross-referenced against `grep -n "^## DR-"` to
map every match to its exact DR number (the task's line-index table was verified correct against
this), then read DR-0, DR-1(partial), DR-2, DR-3, DR-5, DR-20, DR-22, DR-23, DR-24, DR-25, DR-26,
DR-27, DR-29, DR-32, DR-33, DR-37, DR-38, plus Appendix B and Appendix C in full.

## X-SEC-security.md — DRs applied: DR-0, DR-3, DR-5, DR-20, DR-22, DR-23, DR-24, DR-25, DR-26,
DR-27, DR-29, DR-37, DR-38 (426 → 613 lines)

Applied:
- Header line added listing all DRs.
- §3.1: rewrote FR-XSEC-1 (TLS 1.3 floor, DR-26 §26.3 rule 1), FR-XSEC-3 (capability matrix, not
  hierarchy; execute = approver-or-admin, DR-23 §23.5), FR-XSEC-4/5/6 (cite `01 §8.4` for
  limits/rates instead of restating stale numbers), FR-XSEC-7 (delimited-untrusted + canary
  mechanism, DR-37 §37.1), FR-XSEC-8 (fail-closed hash-chained audit, DR-27), FR-XSEC-9 (Go 1.27.1
  govulncheck, DR-1), FR-XSEC-10 (Slack/Teams principal resolution via `IdentityStore`, DR-25
  §25.4). Added new **FR-XSEC-11 … FR-XSEC-15** verbatim per DR-37 §37.2 / Appendix C.
- §3.2: fixed rate-limiter bound (65 536 keys, not 100k) and removed JWT framing.
- §4.2/§4.3: replaced `Principal`/`roleRank`/`Allow` model with the verbatim DR-25 §25.1 `auth`
  package (`Subject`, `Authenticator`, `Authorizer.Can`, `RateLimiter`, `AuditSink`,
  `SecretSource`, `EgressDialer`, `IdentityStore`, `ChatIdentity`/`IdentityBinding`); added the
  DR-27 §27.1 hash-chain formula verbatim, `audit_anchor` DDL, and anchor config; deleted the
  undefined `PayloadHash` preimage; deleted the JWT/`auth.token_ttl` model in favor of the opaque
  `tiq_<tokenID>_<secret>` token (DR-25 §25.3); fixed config-key list to `auth.tls.min_version` /
  `auth.audit.*` (cited, not valued, per DR-0).
- §4.4 (new): the RBAC capability matrix verbatim from DR-25 §25.2 — "the single table every
  document cites."
- §4.5 (was §4.4 algorithms): rewrote pseudocode to `Subject`/`Can`/capability matrix.
- §4.6 (new): Chat identity binding, DR-25 §25.4, with `identity_binding` DDL, admin endpoints,
  AC-XSEC-14/15.
- §4.7 (new, replaces old inline prompt-injection block): DR-37 §37.1 mechanism verbatim
  (`UntrustedKind`, canary, schema validator), citing `01 §8.6` as owner.
- §4.4a/§4.4b: kept the existing detailed per-component STRIDE grid (not contradicted) and added
  the **mandatory** DR-37 §37.3 one-row-per-package table (10 rows) plus the DR-38 §38.4 note that
  a doc without it fails docs CI.
- §4.8 (was §4.5): fixed "TLS 1.2+" → "TLS 1.3" in both diagrams; pseudocode to `Subject`/`Can`.
- §5/§6/§7/§8: updated failure-mode table, deleted JWT-signing-key note, made the STRIDE promise a
  mechanical gate (DR-38), rewrote tests/ACs to add AC-XSEC-7a–d/12/14/15/16–19/20, **deleted the
  token-revocation open question** (DR-25 §25.3 already answers it) and the "STRIDE table
  incomplete" open question (now mechanically enforced).

Remaining/ambiguous: the STRIDE table format question (per-component grid vs. DR-37's
package/threat/mitigation table) was resolved by keeping both rather than replacing the grid —
DR-37's text ("gains one row per feature package") reads as additive, but a stricter reading could
argue for a full replacement; flagged, not re-decided.

## X-OPS-deployment.md — DRs applied: DR-0, DR-1, DR-2, DR-3, DR-5, DR-24, DR-25, DR-26, DR-32,
DR-33 (393 → 478 lines)

Applied:
- Header line + rewritten Purpose paragraph: `gateway` (not `collector`/DaemonSet), no
  chart-managed database of any kind, `internal/ops` never existed (DR-2/DR-3), backup/restore/
  version-skew are `cmd/traceiq` subcommands not interfaces (DR-3 rejected `BackupManager`/
  `VersionChecker`).
- §3.1: full rewrite of FR-XOPS-1 (dev-mode defaults verbatim from DR-33 §33.4), FR-XOPS-2
  (`gateway` Deployment+HPA 2..50, no DaemonSet, no chart-managed DB), **FR-XOPS-3 deleted in
  full** per DR-5 §5.2 (no header-based tenant resolution exists at all — this contradicted the
  register outright and was the largest single fix), FR-XOPS-4/FR-XOPS-5 rewritten verbatim per
  DR-33 §33.1/§33.3 (readiness rule, per-file RPO, no object-storage probe), FR-XOPS-6/7/8 updated
  for `tenant.Policy`/`PolicyStore` (DR-3) and the `01 §10.1`-owned capacity formulas (DR-32 §32.4).
- §3.2: deleted the "<10% overhead via incremental WAL shipping" NFR (unsound per DR-33 §33.3).
- §4.1 diagram: renamed collector→gateway, removed DaemonSet and the `PGV` Postgres+pgvector box
  (DR-32 §32.1), Kafka shown as optional not mandatory.
- §4.2/§4.3: deleted `package ops` (`TenantResolver`, `ops.PolicyStore`, `BackupManager`,
  `VersionChecker`) entirely; replaced with `internal/tenant`'s `Policy`/`Resolver`/`PolicyStore`
  cited from DR-3 §3, and CLI-subcommand framing for backup/restore/version-check; fixed config
  keys to `ops.backup.*`/`selfobs.metrics_endpoint` and deleted `ops.mode`/`ops.role`/
  `ops.tenant_header_name`.
- §4.4: replaced tenant-resolution pseudocode with the single `FromSubject`/`FromDevDefault` path;
  replaced the capacity table with the DR-32 §32.4 formula set and 4-band table (numbers were
  25–90× too high before — real fix, not cosmetic).
- §4.5 diagrams: renamed collector→gateway; rewrote the backup/restore sequence to the two-interval
  `VACUUM INTO` + cold-WAL model instead of a single "hot-index WAL" model.
- §4.6 (new): DR-33 §33.2 graceful shutdown order (12 steps) + startup reconciliation, verbatim.
- §5/§6/§7/§8: rewrote failure-mode table around the new readiness/backup/tenant model; rewrote
  security considerations (gateway/sampler/brain least-privilege, metrics-endpoint auth gate);
  added AC-XOPS-9/10/11/12 and the DR-32 §32.3 "Decided (round 1)" bus-default note; closed two of
  three open questions (`PolicyStore` backend is `control.db`; DaemonSet over-provisioning question
  moot after Deployment+HPA).

Remaining/ambiguous: none identified as blocking; the capacity-table load-test-validation gap
(pre-GA work) is correctly still open per the register's own wording.

## F09-guarded-remediation.md — verified, no changes needed

Header: `Revision 2 — applies DR-0, DR-3, DR-4, DR-5, DR-22, DR-23, DR-24, DR-25, DR-27, DR-29,
DR-31, DR-37`. Cross-checked against every register "Docs to change" line naming `F09`: DR-3
(§4.2), DR-4 (§4.2), DR-5 (§4.3), DR-22 (§3.1/§4.2/§4.4/§6/§7), DR-23 (full), DR-24 (§3.1 FR-F09-9),
DR-25 (§4.2/§4.3), DR-27 (§3.1 FR-F09-8, §4.2), DR-29 (§4.3), DR-31 (constructor), DR-37 (§6) — the
header list is a complete match, nothing missing. Spot-checked the execute-role correction
(DR-23 §23.5/DR-25): line 416 correctly reads `POST /v1/actions/{id}/execute — approver, admin`.
No edits made.

## F10-natural-language.md — verified, no changes needed

Header: `Revision 2 — applies DR-0, DR-5, DR-16, DR-25, DR-29, DR-31, DR-35, DR-37`. Cross-checked
against every register line naming `F10`: DR-5 (§4.3), DR-16 (§4.4), DR-25 (§4.3), DR-29 (§4.3),
DR-31 (constructor), DR-35 (own DR, full), DR-37 (§4.4) — complete match. No edits made.

## Status summary

| Doc | Status | Lines before → after |
|---|---|---|
| X-SEC-security.md | DONE | 424 → 613 |
| X-OPS-deployment.md | DONE | 393 → 478 |
| F09-guarded-remediation.md | VERIFIED, no gaps | 736 (unchanged) |
| F10-natural-language.md | VERIFIED, no gaps | 505 (unchanged) |

No PARTIAL items. One documented ambiguity (STRIDE table additive-vs-replace in X-SEC), resolved
conservatively by keeping both and flagging it above rather than re-deciding the register.
