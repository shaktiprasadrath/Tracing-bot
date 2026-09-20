// Package ingest holds bounded OTLP ingestion queues, one overflow policy,
// and a measured allocation budget (DR-28). ingest.SpanSink.Consume(ctx,
// model.TenantID, []model.Span) error mentions only model types, so
// topology.LiveGraph satisfies it structurally without ingest ever being
// imported by topology (DR-2's signature rule).
//
// Binding source: docs/architecture/06-decision-register.md, DR-28
// (verbatim Go block).
//
// Feature IDs: F01 (ingestion).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/selfobs.
package ingest
