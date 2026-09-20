// Package api holds the one API surface: the endpoint table, roles, the
// MCP tool set, idempotency (DR-29), and the listeners/TLS/profile/limits
// table (DR-26). It is the sole package permitted to import every feature
// package (DR-2's adjacency table) — it bridges rca.Investigation's
// suggested actions to remediate.Guard.Propose (DR-2's rca-remediate
// cycle-break) and hosts the identity-binding admin endpoints (DR-25
// §25.4).
//
// Binding source: docs/architecture/06-decision-register.md, DR-26 and
// DR-29 (verbatim Go blocks).
//
// Feature IDs: cross-cutting — the HTTP/MCP surface for every F0x feature.
//
// Allowed imports (DR-2's adjacency table): all feature packages,
// internal/auth, internal/tenant, internal/config, internal/selfobs, web.
package api
