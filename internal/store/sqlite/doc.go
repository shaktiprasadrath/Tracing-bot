// Package sqlite is the SQLite driver for store.HotIndex: the two-file,
// two-writer-goroutine hot index (`${data_dir}/hot/traceiq.db` for
// telemetry, `${data_dir}/control/control.db` for control-plane state),
// its covering indexes and its published sizing model (DR-6). It also
// satisfies topology.EdgeSink / topology.EdgeSource (DR-13 — F04 persists
// topology edges through sqlite and warm-starts from it, never by calling
// store.HotIndex directly) and sampler.BaselineSource (DR-10, DR-2's
// cycle-break table — the sampler emits through channels and never
// imports store; cmd/traceiq wires this driver in instead).
//
// Binding source: docs/architecture/06-decision-register.md, DR-6
// (`HotIndex`, schema, PRAGMAs, sizing) and DR-13 (EdgeSink/EdgeSource).
//
// Feature IDs: F01 (ingestion persistence), F03 (retention), F04
// (topology edge persistence and warm start).
//
// Allowed imports (DR-2's adjacency table, plus internal/archtest's
// documented allowance for a store driver subpackage to import its own
// parent): internal/model, internal/config, internal/tenant,
// internal/topology, internal/store.
package sqlite
