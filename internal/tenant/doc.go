// Package tenant is the canonical home for tenancy: the per-tenant Policy
// record and the one Resolver (DR-3). It exists as a dedicated
// leaf-adjacent package — rather than folding Policy into model and the
// resolver into auth, as both round-1 reviewers proposed — because auth
// must *use* a tenant policy (RBAC bindings) while store, sampler and
// remediate must *read* it without importing auth (DR-3's decision
// rationale; see also Appendix B).
//
// Binding source: docs/architecture/06-decision-register.md, DR-3.
//
// Feature IDs: cross-cutting — F02 (sampling floor), F03 (retention/byte
// budget), F06 (LLM spend), F09 (remediation allowlist), X-SEC (RBAC).
//
// Allowed imports (DR-2's adjacency table): internal/model only.
package tenant
