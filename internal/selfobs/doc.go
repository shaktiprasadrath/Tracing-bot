// Package selfobs is TraceIQ's own observability surface: the
// /metrics (Prometheus) and /healthz//readyz exposition, and the optional
// self-tracing OTLP export mentioned in 01 §7's `selfobs` block. The
// register does not print a canonical Go interface for it — DR-9 places it
// only in the RSS derivation's "Go runtime, channels..." line and DR-2's
// adjacency table gives it the narrowest import set of any non-leaf
// package, which is the strongest signal of its role: a pure sink other
// packages report into.
//
// Binding source: docs/architecture/06-decision-register.md, principally
// DR-2 (adjacency), DR-26 §26.2/§26.5 (loopback default, tenant-label
// warning), DR-33 §33.1 (readiness/liveness semantics selfobs exposes).
//
// Feature IDs: cross-cutting (X-OPS).
//
// Allowed imports (DR-2's adjacency table): internal/model only.
package selfobs
