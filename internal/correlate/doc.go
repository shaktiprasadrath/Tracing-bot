// Package correlate holds the tenant-scoped correlation adapters: one
// egress path, bounded fan-out, and a dev adapter (DR-20). The caller
// supplies model.Window and the trace; correlate never calls
// store.GetTrace and never imports internal/store (DR-2's cycle-break
// table).
//
// Binding source: docs/architecture/06-decision-register.md, DR-20
// (verbatim Go block).
//
// Feature IDs: F07 (cross-signal correlation).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/auth.
package correlate
