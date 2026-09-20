// Package sampler holds the canonical sampling interface, a hard keep-rate
// cap, a bounded rare-path, cached baselines (DR-10), the interest
// predicate (DR-11), and cost control (DR-12). The sampler emits through
// channels and reads baselines through the consumer-declared
// sampler.BaselineSource; it never imports internal/store (DR-2's
// cycle-break table) — cmd/traceiq wires a store/sqlite implementation in.
// sampler.Reason is deleted; store.Trace carries model.StorageTier and
// model.KeepReason instead (DR-4).
//
// Binding source: docs/architecture/06-decision-register.md, DR-10, DR-11,
// DR-12 (verbatim Go blocks).
//
// Feature IDs: F02 (adaptive sampling).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant.
package sampler
