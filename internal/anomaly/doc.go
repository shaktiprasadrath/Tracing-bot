// Package anomaly holds the canonical anomaly-detection interfaces and a
// baseline model bounded for real cardinality (DR-14).
//
// Binding source: docs/architecture/06-decision-register.md, DR-14
// (verbatim Go block).
//
// Feature IDs: F05 (anomaly detection).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/topology, internal/store.
package anomaly
