// Package topology holds the live service graph: one input (spans via the
// consumer-declared ingest.SpanSink structural interface, DR-2's signature
// rule — topology never imports ingest), one edge key, a bounded graph,
// and the full Graph interface (DR-13).
//
// Binding source: docs/architecture/06-decision-register.md, DR-13
// (verbatim Go block).
//
// Feature IDs: F04 (topology / service map).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant.
package topology
